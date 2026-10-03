package main

import (
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.listenAddr != ":8080" || cfg.bingBaseURL.String() != "https://www.bing.com" || cfg.httpTimeout != 10*time.Second {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{"LISTEN_ADDR", "127.0.0.1:9090", true},
		{"LISTEN_ADDR", "[::]:9090", true},
		{"LISTEN_ADDR", "8080", false},
		{"LISTEN_ADDR", ":0", false},
		{"LISTEN_ADDR", ":65536", false},
		{"BING_BASE_URL", "https://cn.bing.com/", true},
		{"BING_BASE_URL", "http://localhost:8081", true},
		{"BING_BASE_URL", "file:///tmp", false},
		{"BING_BASE_URL", "https://user:pass@bing.com", false},
		{"BING_BASE_URL", "https://bing.com/path", false},
		{"BING_BASE_URL", "https://bing.com/?q=x", false},
		{"HTTP_TIMEOUT", "15s", true},
		{"HTTP_TIMEOUT", "0s", false},
		{"HTTP_TIMEOUT", "-1s", false},
		{"HTTP_TIMEOUT", "hello", false},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			_, err := parseConfig(func(key string) (string, bool) { return tc.value, key == tc.key })
			if (err == nil) != tc.valid {
				t.Fatalf("config valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}
