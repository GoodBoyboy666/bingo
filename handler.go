package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxArchiveBytes = 1 << 20
	maxImageBytes   = 32 << 20
)

var sizePattern = regexp.MustCompile(`^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$`)

type bingImage struct {
	URLBase       string `json:"urlbase"`
	StartDate     string `json:"startdate"`
	Copyright     string `json:"copyright"`
	CopyrightLink string `json:"copyrightlink"`
}

type imageInfo struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Link  string `json:"link"`
	Time  string `json:"time"`
}

type imageHandler struct {
	cfg    config
	client *http.Client
}

func newHandler(cfg config) http.Handler {
	h := &imageHandler{cfg: cfg, client: &http.Client{Timeout: cfg.httpTimeout}}
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", h.serveImage)
	return mux
}

func (h *imageHandler) serveImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "Invalid query string", http.StatusBadRequest)
		return
	}
	day := 0
	if query.Get("rand") == "true" {
		day = rand.IntN(9) - 1
	} else if value := query.Get("day"); value != "" {
		day, err = strconv.Atoi(value)
		if err != nil || day < -1 || day > 7 {
			http.Error(w, "day must be an integer between -1 and 7", http.StatusBadRequest)
			return
		}
	}
	size := query.Get("size")
	if size == "" {
		size = "1920x1080"
	}
	if size != "UHD" && !sizePattern.MatchString(size) {
		http.Error(w, "size must be UHD or a resolution such as 1920x1080", http.StatusBadRequest)
		return
	}
	wantInfo := query.Get("info") == "true"
	wantProxy := query.Get("direct") == "false" && !wantInfo

	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.httpTimeout)
	defer cancel()
	info, err := h.fetchInfo(ctx, day, size)
	if err != nil {
		upstreamError(w, err)
		return
	}
	if wantInfo {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method != http.MethodHead {
			if err := json.NewEncoder(w).Encode(info); err != nil {
				log.Printf("write image info: %v", err)
			}
		}
		return
	}
	if !wantProxy {
		http.Redirect(w, r, info.URL, http.StatusFound)
		return
	}
	if err := h.proxyImage(ctx, w, r.Method, info.URL); err != nil {
		upstreamError(w, err)
	}
}

func (h *imageHandler) fetchInfo(ctx context.Context, day int, size string) (imageInfo, error) {
	endpoint := h.cfg.bingBaseURL.ResolveReference(&url.URL{Path: "/HPImageArchive.aspx"})
	endpoint.RawQuery = url.Values{"format": {"js"}, "idx": {strconv.Itoa(day)}, "n": {"1"}}.Encode()
	response, err := h.fetch(ctx, http.MethodGet, endpoint.String())
	if err != nil {
		return imageInfo{}, err
	}
	defer response.Body.Close()
	body, err := readLimited(response.Body, maxArchiveBytes)
	if err != nil {
		return imageInfo{}, fmt.Errorf("read Bing archive: %w", err)
	}
	var archive struct {
		Images []bingImage `json:"images"`
	}
	if err := json.Unmarshal(body, &archive); err != nil {
		return imageInfo{}, fmt.Errorf("decode Bing archive: %w", err)
	}
	if len(archive.Images) == 0 {
		return imageInfo{}, fmt.Errorf("Bing archive contains no images")
	}
	img := archive.Images[0]
	base, err := url.Parse(img.URLBase)
	if err != nil || base.IsAbs() || base.Host != "" || !strings.HasPrefix(base.Path, "/") ||
		strings.HasPrefix(base.Path, "//") || base.Fragment != "" {
		return imageInfo{}, fmt.Errorf("Bing archive contains an invalid image URL")
	}
	imageURL := h.cfg.bingBaseURL.ResolveReference(base).String() + "_" + size + ".jpg"
	if size == "UHD" {
		imageURL += "&w=3840&h=2160&c=8&rs=1&o=3&r=0"
	}
	return imageInfo{Title: img.Copyright, URL: imageURL, Link: img.CopyrightLink, Time: img.StartDate}, nil
}

func (h *imageHandler) proxyImage(ctx context.Context, w http.ResponseWriter, method, imageURL string) error {
	response, err := h.fetch(ctx, method, imageURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var body []byte
	if method != http.MethodHead {
		body, err = readLimited(response.Body, maxImageBytes)
		if err != nil {
			return fmt.Errorf("read Bing image: %w", err)
		}
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" && method != http.MethodHead {
		contentType = http.DetectContentType(body)
	}
	if contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "image/jpeg" {
			return fmt.Errorf("Bing image has unexpected Content-Type %q", contentType)
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	if method == http.MethodHead {
		if response.ContentLength >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
		}
		return nil
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if _, err := w.Write(body); err != nil {
		// Headers have been sent; an additional error response would corrupt the image.
		log.Printf("write Bing image: %v", err)
	}
	return nil
}

func (h *imageHandler) fetch(ctx context.Context, method, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	response, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Bing returned HTTP %d", response.StatusCode)
	}
	return response, nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("upstream response exceeds %d bytes", limit)
	}
	return body, nil
}

func upstreamError(w http.ResponseWriter, err error) {
	log.Printf("Bing request failed: %v", err)
	status := http.StatusBadGateway
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	http.Error(w, http.StatusText(status), status)
}
