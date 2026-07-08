package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthURL(t *testing.T) {
	valid := map[string]string{
		":9101":          "http://127.0.0.1:9101/healthz",
		"0.0.0.0:9101":   "http://127.0.0.1:9101/healthz",
		"127.0.0.1:8080": "http://127.0.0.1:8080/healthz",
		"example:8080":   "http://example:8080/healthz",
	}
	for addr, want := range valid {
		got, err := healthURL(addr)
		if err != nil {
			t.Errorf("healthURL(%q) unexpected error: %v", addr, err)
			continue
		}
		if got != want {
			t.Errorf("healthURL(%q) = %q, want %q", addr, got, want)
		}
	}

	if _, err := healthURL("no-port"); err == nil {
		t.Error("healthURL(no-port): expected error, got nil")
	}
}

func TestProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if got := probe(context.Background(), ok.URL); got != 0 {
		t.Errorf("probe(200) = %d, want 0", got)
	}

	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	if got := probe(context.Background(), unavailable.URL); got != 1 {
		t.Errorf("probe(503) = %d, want 1", got)
	}

	// A closed server yields a connection error.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	if got := probe(context.Background(), closedURL); got != 1 {
		t.Errorf("probe(closed) = %d, want 1", got)
	}
}
