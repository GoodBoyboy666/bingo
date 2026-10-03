package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRejectsInvalidImageURLBase(t *testing.T) {
	for _, urlbase := range []string{
		"/redirect?url=https://example.com/",
		"/th?id=OHR.Example&url=https://example.com/",
		"/th?id=OHR.Example&id=OHR.Other",
		"/th?id=https%3A%2F%2Fexample.com",
		"/th?id=OHR.Example%23%40example.com",
		"/th?id=OHR.Example#https://example.com/",
		"/%74h?id=OHR.Example",
		"/th?id=bad%ZZ",
		"/th",
		`\\example.com\th?id=OHR.Example`,
	} {
		t.Run(urlbase, func(t *testing.T) {
			var imageRequests atomic.Int32
			handler := mockHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/HPImageArchive.aspx" {
					writeArchive(w, urlbase)
					return
				}
				imageRequests.Add(1)
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write(testJPEG)
			}, nil)
			for _, target := range []string{"/", "/?direct=false", "/?info=true"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
				if response.Code != http.StatusBadGateway || response.Header().Get("Location") != "" {
					t.Errorf("%s accepted invalid image URL: %d %v", target, response.Code, response.Header())
				}
			}
			if imageRequests.Load() != 0 {
				t.Errorf("invalid metadata triggered %d image requests", imageRequests.Load())
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestOutboundURLValidation(t *testing.T) {
	cfg, err := parseConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target  string
		allowed bool
	}{
		{"https://www.bing.com/th?id=OHR.Example_1920x1080.jpg", true},
		{"https://www.bing.com/HPImageArchive.aspx?format=js&idx=0&n=1", true},
		{"http://www.bing.com/th?id=OHR.Example", false},
		{"https://www.bing.com:8443/th?id=OHR.Example", false},
		{"https://www.bing.com.example.com/th?id=OHR.Example", false},
		{"https://127.0.0.1/th?id=OHR.Example", false},
		{"http://169.254.169.254/latest/meta-data/", false},
		{"https://user@www.bing.com/th?id=OHR.Example", false},
		{"//www.bing.com/th?id=OHR.Example", false},
		{"https://www.bing.com/redirect?url=https://example.com", false},
		{"https://www.bing.com/th?id=OHR.Example#fragment", false},
		{"https://www.bing.com/%74h?id=OHR.Example", false},
		{`https://www.bing.com\@example.com/th?id=OHR.Example`, false},
	} {
		t.Run(tc.target, func(t *testing.T) {
			calls := 0
			h := &imageHandler{cfg: cfg, client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
			})}}
			response, err := h.fetch(context.Background(), http.MethodGet, tc.target)
			if response != nil {
				response.Body.Close()
			}
			if tc.allowed {
				if err != nil || calls != 1 {
					t.Errorf("allowed request: error=%v, calls=%d", err, calls)
				}
			} else if err == nil || calls != 0 {
				t.Errorf("disallowed target reached transport: error=%v, calls=%d", err, calls)
			}
		})
	}
}

func TestUpstreamRedirectPolicy(t *testing.T) {
	var externalCalls atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalCalls.Add(1)
		writeArchive(w, "/th?id=OHR.Example")
	}))
	t.Cleanup(external.Close)

	for _, tc := range []struct {
		name, stage, destination string
		allowed                  bool
	}{
		{"archive same origin", "archive", "same", true},
		{"image same origin", "image", "same", true},
		{"archive external origin", "archive", "external", false},
		{"image external origin", "image", "external", false},
		{"archive unexpected endpoint", "archive", "other", false},
		{"image unexpected endpoint", "image", "other", false},
		{"archive redirect loop", "archive", "loop", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var otherCalls, archiveCalls atomic.Int32
			handler := mockHandler(t, func(w http.ResponseWriter, r *http.Request) {
				isArchive := r.URL.Path == "/HPImageArchive.aspx"
				if isArchive {
					archiveCalls.Add(1)
				} else if r.URL.Path != "/th" {
					otherCalls.Add(1)
				}
				redirectStage := (isArchive && tc.stage == "archive") || (r.URL.Path == "/th" && tc.stage == "image")
				if redirectStage && (r.URL.Query().Get("redirected") != "1" || tc.destination == "loop") {
					location := r.URL.Path + "?redirected=1"
					switch tc.destination {
					case "external":
						location = external.URL + "/th?id=OHR.Example"
					case "other":
						location = "/unexpected?redirected=1"
					}
					w.Header().Set("Location", location)
					w.WriteHeader(http.StatusFound)
					return
				}
				if isArchive {
					writeArchive(w, "/th?id=OHR.Example")
					return
				}
				w.Header().Set("Content-Type", "image/jpeg")
				_, _ = w.Write(testJPEG)
			}, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?direct=false", nil))
			if tc.allowed {
				if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), testJPEG) {
					t.Errorf("allowed redirect failed: %d %q", response.Code, response.Body.Bytes())
				}
			} else if response.Code != http.StatusBadGateway || response.Header().Get("Location") != "" {
				t.Errorf("disallowed redirect response: %d %v", response.Code, response.Header())
			}
			if otherCalls.Load() != 0 {
				t.Errorf("redirect reached disallowed endpoint %d times", otherCalls.Load())
			}
			if tc.destination == "loop" && archiveCalls.Load() != 10 {
				t.Errorf("redirect loop made %d requests, want 10", archiveCalls.Load())
			}
		})
	}
	if externalCalls.Load() != 0 {
		t.Errorf("redirects reached external server %d times", externalCalls.Load())
	}
}
