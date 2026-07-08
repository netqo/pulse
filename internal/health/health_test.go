package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
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

func TestHandler(t *testing.T) {
	reg := prometheus.NewRegistry()

	t.Run("healthz is always ok", func(t *testing.T) {
		h := Handler(reg, func(context.Context) error { return errors.New("not ready") })
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("/healthz = %d, want %d", rec.Code, http.StatusOK)
		}
	})

	t.Run("readyz reflects readiness", func(t *testing.T) {
		ready := Handler(reg, func(context.Context) error { return nil })
		rec := httptest.NewRecorder()
		ready.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("/readyz (ready) = %d, want %d", rec.Code, http.StatusOK)
		}

		notReady := Handler(reg, func(context.Context) error { return errors.New("dependency down") })
		rec = httptest.NewRecorder()
		notReady.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("/readyz (not ready) = %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("metrics is served", func(t *testing.T) {
		h := Handler(reg, func(context.Context) error { return nil })
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("/metrics = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}
