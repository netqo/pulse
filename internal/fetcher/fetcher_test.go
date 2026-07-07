package fetcher

import (
	"testing"

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
