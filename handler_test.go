package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

var testJPEG = []byte{0xff, 0xd8, 0xff, 0xe0, 0, 2, 0xff, 0xd9}

func mockHandler(t *testing.T, upstream http.HandlerFunc, overrides map[string]string) http.Handler {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	values := map[string]string{"BING_BASE_URL": server.URL}
	for key, value := range overrides {
		values[key] = value
	}
	cfg, err := parseConfig(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	return newHandler(cfg)
}

func writeArchive(w http.ResponseWriter, urlbase string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"images": []bingImage{{
		URLBase: urlbase, StartDate: "20261003", Copyright: "壁纸 \"标题\" & 版权", CopyrightLink: "https://www.bing.com/search?q=example",
	}}})
}

func TestImageResponses(t *testing.T) {
	for _, tc := range []struct {
		name, target, day, size string
		info                    bool
	}{
		{name: "default redirect", target: "/", day: "0", size: "1920x1080"},
		{name: "specified date", target: "/?day=1", day: "1", size: "1920x1080"},
		{name: "negative date", target: "/?day=-1", day: "-1", size: "1920x1080"},
		{name: "last date and resolution", target: "/?day=7&size=1366x768", day: "7", size: "1366x768"},
		{name: "UHD redirect", target: "/?size=UHD", day: "0", size: "UHD"},
		{name: "JSON", target: "/?info=true", day: "0", size: "1920x1080", info: true},
		{name: "JSON UHD takes precedence over proxy", target: "/?info=true&direct=false&size=UHD", day: "0", size: "UHD", info: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstreamOrigin := make(chan string, 1)
			handler := mockHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/HPImageArchive.aspx" || r.URL.Query().Get("format") != "js" ||
					r.URL.Query().Get("n") != "1" || r.URL.Query().Get("idx") != tc.day {
					t.Errorf("unexpected archive request: %s", r.URL)
				}
				upstreamOrigin <- "http://" + r.Host
				writeArchive(w, "/th?id=OHR.Example")
			}, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.target, nil))
			wantURL := <-upstreamOrigin + "/th?id=OHR.Example_" + tc.size + ".jpg"
			if tc.size == "UHD" {
				wantURL += "&w=3840&h=2160&c=8&rs=1&o=3&r=0"
			}
			if tc.info {
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
					t.Fatalf("unexpected JSON response: %d %v", response.Code, response.Header())
				}
				var got imageInfo
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				want := imageInfo{Title: "壁纸 \"标题\" & 版权", URL: wantURL, Link: "https://www.bing.com/search?q=example", Time: "20261003"}
				if got != want {
					t.Errorf("info = %#v, want %#v", got, want)
				}
			} else if response.Code != http.StatusFound || response.Header().Get("Location") != wantURL {
				t.Errorf("redirect = %d %q, want 302 %q", response.Code, response.Header().Get("Location"), wantURL)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Error("wallpaper responses must not be cached")
			}
		})
	}
}

func TestRandomOverridesDay(t *testing.T) {
	handler := mockHandler(t, func(w http.ResponseWriter, r *http.Request) {
		day, err := strconv.Atoi(r.URL.Query().Get("idx"))
		if err != nil || day < -1 || day > 7 {
			t.Errorf("random index out of range: %q", r.URL.Query().Get("idx"))
		}
		writeArchive(w, "/th?id=OHR.Example")
	}, nil)
	for range 20 {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?rand=true&day=invalid&info=true", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("random response: %d %s", response.Code, response.Body)
		}
	}
}

func TestProxyImage(t *testing.T) {
	for _, tc := range []struct {
		name, referer, method string
	}{
		{name: "missing referer", method: "GET"},
		{name: "other domain", referer: "https://example.com/", method: "GET"},
		{name: "malformed referer", referer: "%invalid", method: "GET"},
		{name: "HEAD without referer", method: "HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := mockHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/HPImageArchive.aspx" {
					writeArchive(w, "/th?id=OHR.Example")
					return
				}
				if r.URL.Path != "/th" || r.URL.Query().Get("id") != "OHR.Example_1920x1080.jpg" || r.Method != tc.method {
					t.Errorf("unexpected image request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "image/jpeg")
				w.Header().Set("Content-Length", strconv.Itoa(len(testJPEG)))
				if r.Method != http.MethodHead {
					_, _ = w.Write(testJPEG)
				}
			}, nil)
			request := httptest.NewRequest(tc.method, "/?direct=false", nil)
			request.Header.Set("Referer", tc.referer)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" {
				t.Fatalf("proxy response: %d %v %s", response.Code, response.Header(), response.Body)
			}
			wantBody := testJPEG
			if tc.method == http.MethodHead {
				wantBody = nil
			}
			if !bytes.Equal(response.Body.Bytes(), wantBody) {
				t.Errorf("unexpected proxy body %q", response.Body.Bytes())
			}
		})
	}
}

func TestRejectedRequests(t *testing.T) {
	var upstreamCalls atomic.Int32
	handler := mockHandler(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		writeArchive(w, "/th?id=OHR.Example")
	}, nil)
	for _, tc := range []struct {
		method, target string
		status         int
	}{
		{"GET", "/?day=8", 400},
		{"GET", "/?day=-2", 400},
		{"GET", "/?day=abc", 400},
		{"GET", "/?size=uhd", 400},
		{"GET", "/?size=0x1080", 400},
		{"GET", "/?size=1920x1080%26url=evil", 400},
		{"GET", "/?day=%ZZ", 400},
		{"POST", "/", 405},
		{"GET", "/missing", 404},
		{"GET", "/index.php", 404},
		{"GET", "/index.php/extra", 404},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.target, nil))
		if response.Code != tc.status {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.target, response.Code, tc.status)
		}
		if tc.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") != "GET, HEAD" {
			t.Error("missing Allow header")
		}
	}
	if upstreamCalls.Load() != 0 {
		t.Errorf("invalid requests made %d upstream calls", upstreamCalls.Load())
	}
}

func TestUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		upstream http.HandlerFunc
		status   int
		proxy    bool
	}{
		{name: "HTTP error", status: 502, upstream: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }},
		{name: "invalid JSON", status: 502, upstream: func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "invalid") }},
		{name: "empty images", status: 502, upstream: func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"images":[]}`) }},
		{name: "absolute image URL", status: 502, upstream: func(w http.ResponseWriter, r *http.Request) { writeArchive(w, "https://example.com/evil") }},
		{name: "protocol relative image URL", status: 502, upstream: func(w http.ResponseWriter, r *http.Request) { writeArchive(w, "//example.com/evil") }},
		{name: "missing image URL", status: 502, upstream: func(w http.ResponseWriter, r *http.Request) { writeArchive(w, "") }},
		{name: "oversized archive", status: 502, upstream: func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", maxArchiveBytes+1))
		}},
		{name: "timeout", status: 504, upstream: func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }},
		{name: "image HTTP error", status: 502, proxy: true, upstream: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/HPImageArchive.aspx" {
				writeArchive(w, "/th?id=OHR.Example")
			} else {
				w.WriteHeader(404)
			}
		}},
		{name: "HTML instead of image", status: 502, proxy: true, upstream: func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/HPImageArchive.aspx" {
				writeArchive(w, "/th?id=OHR.Example")
			} else {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, "<html>Error</html>")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			if tc.name == "timeout" {
				values["HTTP_TIMEOUT"] = "50ms"
			}
			handler := mockHandler(t, tc.upstream, values)
			target := "/?info=true"
			if tc.proxy {
				target = "/?direct=false"
			}
			request := httptest.NewRequest(http.MethodGet, target, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || response.Body.String() != fmt.Sprintf("%s\n", http.StatusText(tc.status)) {
				t.Errorf("upstream failure response = %d %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestInfoHead(t *testing.T) {
	handler := mockHandler(t, func(w http.ResponseWriter, r *http.Request) { writeArchive(w, "/th?id=OHR.Example") }, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/?info=true", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("HEAD info response = %d %v %q", response.Code, response.Header(), response.Body.String())
	}
}
