// Package api serves the read side of Pulse over HTTP: instrument reference data
// and price observations (latest and historical series) backed by the
// PostgreSQL access layer. It exposes only reads; ingestion and enrichment live
// in the fetcher and processor services.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/db"
)

// Reader is the read-side data access the API depends on. *db.DB satisfies it;
// tests supply a fake.
type Reader interface {
	ListInstruments(ctx context.Context) ([]db.Instrument, error)
	LatestPrice(ctx context.Context, symbol string) (db.PricePoint, error)
	PriceSeries(ctx context.Context, symbol string, from, to time.Time, limit int) ([]db.PricePoint, error)
}

// Series query bounds applied when the caller omits or overshoots them.
const (
	defaultSeriesLimit  = 1000
	maxSeriesLimit      = 5000
	defaultSeriesWindow = 24 * time.Hour
)

// Server wires the read data source to the HTTP handlers.
type Server struct {
	reader  Reader
	logger  *slog.Logger
	metrics *metrics
}

// New constructs a Server and registers its request metrics with reg.
func New(reader Reader, logger *slog.Logger, reg prometheus.Registerer) *Server {
	return &Server{reader: reader, logger: logger, metrics: newMetrics(reg)}
}

// Handler returns the API router wrapped with its middleware. Routes use
// method-and-path patterns and a {symbol} path parameter. The chain is
// metrics -> panic recovery -> mux, so a panicking handler is converted to a
// logged 500 that the outer metrics layer still observes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/instruments", s.handleListInstruments)
	mux.HandleFunc("GET /api/v1/instruments/{symbol}/latest", s.handleLatestPrice)
	mux.HandleFunc("GET /api/v1/instruments/{symbol}/prices", s.handlePriceSeries)
	return s.metrics.instrument(s.recoverer(mux))
}

// recoverer converts a panic in a downstream handler into a logged 500 JSON
// response instead of letting it reach the server's default (unstructured)
// recovery, which would drop the connection and bypass the structured logger.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic recovered", "method", r.Method, "path", r.URL.Path, "panic", rec)
				writeJSON(w, http.StatusInternalServerError, errorDTO{Error: "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
