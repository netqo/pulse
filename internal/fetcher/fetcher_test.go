package fetcher

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func newTestFetcher(t *testing.T, wsURL string, symbols []string) *Fetcher {
	t.Helper()
	return New(wsURL, symbols, nil, nil, prometheus.NewRegistry())
}

func TestStreamURL(t *testing.T) {
	f := newTestFetcher(t, "wss://stream.binance.com:9443/stream", []string{"BTCUSDT", "ethusdt"})

	got, err := f.streamURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "wss://stream.binance.com:9443/stream?streams=btcusdt@trade/ethusdt@trade"
	if got != want {
		t.Errorf("streamURL() = %q, want %q", got, want)
	}
}

func TestStreamURLTrailingSlash(t *testing.T) {
	f := newTestFetcher(t, "wss://stream.binance.com:9443/stream/", []string{"BTCUSDT"})

	got, err := f.streamURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "wss://stream.binance.com:9443/stream?streams=btcusdt@trade"
	if got != want {
		t.Errorf("streamURL() = %q, want %q", got, want)
	}
}

func TestStreamURLNoSymbols(t *testing.T) {
	f := newTestFetcher(t, "wss://stream.binance.com:9443/stream", nil)

	if _, err := f.streamURL(); err == nil {
		t.Error("expected error for empty symbol list, got nil")
	}
}

func TestReadyDefaultsFalse(t *testing.T) {
	f := newTestFetcher(t, "wss://stream.binance.com:9443/stream", []string{"BTCUSDT"})
	if f.Ready() {
		t.Error("Ready() = true before connecting, want false")
	}
}

func TestNextBackoff(t *testing.T) {
	const ceiling = 30 * time.Second
	tests := []struct {
		current, want time.Duration
	}{
		{1 * time.Second, 2 * time.Second},
		{8 * time.Second, 16 * time.Second},
		{20 * time.Second, ceiling}, // doubling would exceed the cap
		{ceiling, ceiling},          // already at the cap
	}
	for _, tc := range tests {
		if got := nextBackoff(tc.current, ceiling); got != tc.want {
			t.Errorf("nextBackoff(%v, %v) = %v, want %v", tc.current, ceiling, got, tc.want)
		}
	}
}
