package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/db"
)

func TestSanitizeLogPathKeepsOrdinaryPath(t *testing.T) {
	const path = "/api/v1/prices/BTCUSDT"
	if got := sanitizeLogPath(path); got != path {
		t.Errorf("sanitizeLogPath(%q) = %q, want it unchanged", path, got)
	}
}

func TestSanitizeLogPathStripsControlCharacters(t *testing.T) {
	// A caller reaches these bytes through percent-encoding: net/url rejects a
	// raw newline in the request line, but decodes %0A straight into URL.Path.
	got := sanitizeLogPath("/api/v1/prices/BTC\n{\"level\":\"ERROR\"}\r\x1b[31m")
	want := "/api/v1/prices/BTC{\"level\":\"ERROR\"}[31m"
	if got != want {
		t.Errorf("sanitizeLogPath() = %q, want %q", got, want)
	}
}

func TestSanitizeLogPathTruncatesOverlongPath(t *testing.T) {
	got := sanitizeLogPath("/" + strings.Repeat("a", 4096))
	if len(got) > maxLoggedPathLen+len(truncationMarker) {
		t.Errorf("sanitizeLogPath() kept %d bytes, want at most %d", len(got), maxLoggedPathLen+len(truncationMarker))
	}
	if !strings.HasSuffix(got, truncationMarker) {
		t.Errorf("sanitizeLogPath() = %q, want it to end in %q", got, truncationMarker)
	}
}

func TestSanitizeLogPathDropsInvalidUTF8(t *testing.T) {
	// Truncation cuts on a byte boundary, so it can split a multi-byte rune. The
	// remaining fragment must not reach the log as a replacement character.
	got := sanitizeLogPath("/ok\xff\xfe")
	if got != "/ok" {
		t.Errorf("sanitizeLogPath() = %q, want %q", got, "/ok")
	}
}

func TestRequestAttrsReportsMatchedRoute(t *testing.T) {
	var got []any
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/prices/{symbol}", func(_ http.ResponseWriter, r *http.Request) {
		got = requestAttrs(r)
	})
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/prices/BTCUSDT", nil))

	assertAttr(t, got, "method", http.MethodGet)
	assertAttr(t, got, "route", "GET /api/v1/prices/{symbol}")
	assertAttr(t, got, "path", "/api/v1/prices/BTCUSDT")
}

func TestRequestAttrsLabelsUnmatchedRouteAsOther(t *testing.T) {
	// An unmatched request carries no pattern, and its path is entirely
	// caller-chosen, so it must not become an unbounded log or metric label.
	got := requestAttrs(httptest.NewRequest(http.MethodGet, "/no/such/route", nil))
	assertAttr(t, got, "route", unmatchedRoute)
}

func TestRequestAttrsSanitizesPath(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/prices/BTC%0A%1B%5B31m", nil)
	if !strings.ContainsRune(r.URL.Path, '\n') {
		t.Fatalf("precondition: URL.Path = %q, want it to carry the decoded newline", r.URL.Path)
	}
	assertAttr(t, requestAttrs(r), "path", "/api/v1/prices/BTC[31m")
}

func TestWriteErrorLogsRouteAndSanitizedPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := New(Config{
		Reader: &fakeReader{
			latestFn: func(context.Context, string) (db.PricePoint, error) {
				return db.PricePoint{}, errors.New("boom")
			},
		},
		Logger:     logger,
		Registerer: prometheus.NewRegistry(),
	}).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/instruments/BTC%0A%1B%5B31m/latest", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("decode log record %q: %v", buf.String(), err)
	}
	if got := record["route"]; got != "GET /api/v1/instruments/{symbol}/latest" {
		t.Errorf("route = %v, want the matched pattern", got)
	}
	path, _ := record["path"].(string)
	if strings.ContainsFunc(path, func(r rune) bool { return !unicode.IsPrint(r) }) {
		t.Errorf("path = %q, want no non-printable runes", path)
	}
}

// assertAttr fails the test unless attrs carries key with the given value, where
// attrs is a flat slog-style key/value sequence.
func assertAttr(t *testing.T, attrs []any, key, want string) {
	t.Helper()
	for i := 0; i+1 < len(attrs); i += 2 {
		if attrs[i] == key {
			if got, ok := attrs[i+1].(string); !ok || got != want {
				t.Errorf("attr %q = %v, want %q", key, attrs[i+1], want)
			}
			return
		}
	}
	t.Errorf("attrs %v missing key %q", attrs, key)
}
