package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/db"
)

// fakeReader implements Reader with per-method hooks so each test controls the
// data-layer behavior it exercises.
type fakeReader struct {
	listFn   func(ctx context.Context) ([]db.Instrument, error)
	latestFn func(ctx context.Context, symbol string) (db.PricePoint, error)
	seriesFn func(ctx context.Context, symbol string, from, to time.Time, limit int) ([]db.PricePoint, error)
}

func (f *fakeReader) ListInstruments(ctx context.Context) ([]db.Instrument, error) {
	return f.listFn(ctx)
}

func (f *fakeReader) LatestPrice(ctx context.Context, symbol string) (db.PricePoint, error) {
	return f.latestFn(ctx, symbol)
}

func (f *fakeReader) PriceSeries(ctx context.Context, symbol string, from, to time.Time, limit int) ([]db.PricePoint, error) {
	return f.seriesFn(ctx, symbol, from, to, limit)
}

func newTestServer(t *testing.T, reader Reader) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Config{Reader: reader, Logger: logger, Registerer: prometheus.NewRegistry()}).Handler()
}

func strptr(s string) *string { return &s }

func do(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestListInstruments(t *testing.T) {
	h := newTestServer(t, &fakeReader{
		listFn: func(context.Context) ([]db.Instrument, error) {
			return []db.Instrument{
				{Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", Source: "binance", IsActive: true},
				{Symbol: "ETHUSDT", BaseAsset: "ETH", QuoteAsset: "USDT", Source: "binance", IsActive: true},
			}, nil
		},
	})

	rec := do(t, h, "/api/v1/instruments")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body listInstrumentsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Instruments) != 2 || body.Instruments[0].Symbol != "BTCUSDT" {
		t.Errorf("unexpected instruments: %+v", body.Instruments)
	}
}

func TestLatestPriceOK(t *testing.T) {
	h := newTestServer(t, &fakeReader{
		latestFn: func(_ context.Context, symbol string) (db.PricePoint, error) {
			if symbol != "BTCUSDT" {
				t.Errorf("symbol = %q, want normalized BTCUSDT", symbol)
			}
			return db.PricePoint{
				Ts:    time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC),
				Price: "12345.67000000",
				MA20:  strptr("12000.00000000"),
			}, nil
		},
	})

	// Lower-case path exercises symbol normalization.
	rec := do(t, h, "/api/v1/instruments/btcusdt/latest")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body latestPriceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Symbol != "BTCUSDT" || body.Price != "12345.67000000" {
		t.Errorf("unexpected latest: %+v", body)
	}
	if body.MA20 == nil || *body.MA20 != "12000.00000000" {
		t.Errorf("ma_20 = %v, want 12000.00000000", body.MA20)
	}
	if body.Volume != nil {
		t.Errorf("volume = %v, want null", body.Volume)
	}
}

func TestLatestPriceNotFound(t *testing.T) {
	h := newTestServer(t, &fakeReader{
		latestFn: func(context.Context, string) (db.PricePoint, error) {
			return db.PricePoint{}, db.ErrNotFound
		},
	})
	rec := do(t, h, "/api/v1/instruments/NOPE/latest")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestLatestPriceInternalError(t *testing.T) {
	h := newTestServer(t, &fakeReader{
		latestFn: func(context.Context, string) (db.PricePoint, error) {
			return db.PricePoint{}, errors.New("boom")
		},
	})
	rec := do(t, h, "/api/v1/instruments/BTCUSDT/latest")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body errorDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != "internal error" {
		t.Errorf("error = %q, want opaque 'internal error'", body.Error)
	}
}

func TestPriceSeriesDefaults(t *testing.T) {
	var gotFrom, gotTo time.Time
	var gotLimit int
	h := newTestServer(t, &fakeReader{
		seriesFn: func(_ context.Context, _ string, from, to time.Time, limit int) ([]db.PricePoint, error) {
			gotFrom, gotTo, gotLimit = from, to, limit
			return []db.PricePoint{{Ts: to, Price: "1.00000000"}}, nil
		},
	})

	rec := do(t, h, "/api/v1/instruments/BTCUSDT/prices")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotLimit != defaultSeriesLimit {
		t.Errorf("limit = %d, want default %d", gotLimit, defaultSeriesLimit)
	}
	if gotTo.Sub(gotFrom) != defaultSeriesWindow {
		t.Errorf("default window = %v, want %v", gotTo.Sub(gotFrom), defaultSeriesWindow)
	}
	var body priceSeriesDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 || len(body.Prices) != 1 {
		t.Errorf("count = %d, prices = %d, want 1/1", body.Count, len(body.Prices))
	}
}

func TestPriceSeriesExplicitParamsAndClamp(t *testing.T) {
	var gotFrom, gotTo time.Time
	var gotLimit int
	h := newTestServer(t, &fakeReader{
		seriesFn: func(_ context.Context, _ string, from, to time.Time, limit int) ([]db.PricePoint, error) {
			gotFrom, gotTo, gotLimit = from, to, limit
			return nil, nil
		},
	})

	rec := do(t, h, "/api/v1/instruments/BTCUSDT/prices?from=2025-06-01T00:00:00Z&to=2025-06-02T00:00:00Z&limit=99999")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !gotFrom.Equal(time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("from = %v, want 2025-06-01T00:00:00Z", gotFrom)
	}
	if !gotTo.Equal(time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("to = %v, want 2025-06-02T00:00:00Z", gotTo)
	}
	if gotLimit != maxSeriesLimit {
		t.Errorf("limit = %d, want clamped to %d", gotLimit, maxSeriesLimit)
	}
	// An empty result set must still serialize as [], not null.
	var body priceSeriesDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Prices == nil {
		t.Error("prices should serialize as [] when empty")
	}
}

func TestPanicRecovery(t *testing.T) {
	h := newTestServer(t, &fakeReader{
		latestFn: func(context.Context, string) (db.PricePoint, error) {
			panic("boom")
		},
	})
	rec := do(t, h, "/api/v1/instruments/BTCUSDT/latest")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 after recovered panic", rec.Code)
	}
	var body errorDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != "internal error" {
		t.Errorf("error = %q, want 'internal error'", body.Error)
	}
}

func TestPriceSeriesValidation(t *testing.T) {
	h := newTestServer(t, &fakeReader{
		seriesFn: func(context.Context, string, time.Time, time.Time, int) ([]db.PricePoint, error) {
			t.Error("reader should not be called on invalid input")
			return nil, nil
		},
	})

	cases := map[string]string{
		"bad from":          "/api/v1/instruments/BTCUSDT/prices?from=not-a-time",
		"from after to":     "/api/v1/instruments/BTCUSDT/prices?from=2025-06-02T00:00:00Z&to=2025-06-01T00:00:00Z",
		"non-integer limit": "/api/v1/instruments/BTCUSDT/prices?limit=abc",
		"zero limit":        "/api/v1/instruments/BTCUSDT/prices?limit=0",
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, target)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}
