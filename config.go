package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

type config struct {
	listenAddr  string
	bingBaseURL *url.URL
	httpTimeout time.Duration
}

func loadConfig() (config, error) {
	return parseConfig(os.LookupEnv)
}

func parseConfig(lookup func(string) (string, bool)) (config, error) {
	get := func(key, fallback string) string {
		if value, ok := lookup(key); ok {
			return value
		}
		return fallback
	}

	cfg := config{listenAddr: get("LISTEN_ADDR", ":8080")}
	_, port, err := net.SplitHostPort(cfg.listenAddr)
	if err != nil {
		return cfg, fmt.Errorf("invalid LISTEN_ADDR: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return cfg, fmt.Errorf("LISTEN_ADDR must contain a port between 1 and 65535")
	}

	cfg.bingBaseURL, err = url.Parse(get("BING_BASE_URL", "https://www.bing.com"))
	if err != nil {
		return cfg, fmt.Errorf("invalid BING_BASE_URL: %w", err)
	}
	base := cfg.bingBaseURL
	if (base.Scheme != "https" && base.Scheme != "http") || base.Hostname() == "" || base.User != nil ||
		(base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return cfg, fmt.Errorf("BING_BASE_URL must be an HTTP(S) origin without credentials, path, query or fragment")
	}

	cfg.httpTimeout, err = time.ParseDuration(get("HTTP_TIMEOUT", "10s"))
	if err != nil || cfg.httpTimeout <= 0 {
		return cfg, fmt.Errorf("HTTP_TIMEOUT must be a positive duration, for example 10s")
	}

	return cfg, nil
}
