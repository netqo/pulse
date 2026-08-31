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
	"golang.org/x/time/rate"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/playground"
)

// Reader is the read-side data access the API depends on. *db.DB satisfies it;
// tests supply a fake.
type Reader interface {
	ListInstruments(ctx context.Context) ([]db.Instrument, error)
	LatestPrice(ctx context.Context, symbol string) (db.PricePoint, error)
	PriceSeries(ctx context.Context, symbol string, from, to time.Time, limit int) ([]db.PricePoint, error)
}

// PlaygroundExecutor runs a sandboxed read-only query. *playground.Sandbox
// satisfies it; tests supply a fake.
type PlaygroundExecutor interface {
	Execute(ctx context.Context, query string) (*playground.Result, error)
}

// QueryStore persists and loads shareable Playground queries. *db.DB satisfies
// it; tests supply a fake.
type QueryStore interface {
	SaveQuery(ctx context.Context, in db.SaveQueryInput) (db.SavedQuery, error)
	SavedQuery(ctx context.Context, id string) (db.SavedQuery, error)
}

// AlertStore manages alert rules for the operator management API. *db.DB
// satisfies it; tests supply a fake.
type AlertStore interface {
	InstrumentIDBySymbol(ctx context.Context, symbol string) (int64, error)
	CreateAlertRule(ctx context.Context, in db.CreateAlertRuleInput) (db.AlertRule, error)
	ListAlertRules(ctx context.Context) ([]db.RuleWithSymbol, error)
	DeleteAlertRule(ctx context.Context, id int64) error
}

// Series query bounds applied when the caller omits or overshoots them.
const (
	defaultSeriesLimit  = 1000
	maxSeriesLimit      = 5000
	defaultSeriesWindow = 24 * time.Hour
)

// Playground rate limits. Query runs untrusted SQL bounded by a 5s timeout, so a
// low sustained rate with a small burst is plenty per IP. Save is an open write,
// so it is throttled more tightly to bound abuse; both share the bucket TTL.
const (
	playgroundRate      = rate.Limit(2)
	playgroundBurst     = 5
	playgroundSaveRate  = rate.Limit(1)
	playgroundSaveBurst = 5
	playgroundBucketTTL = 10 * time.Minute
)

// Server wires the read data source, the SQL sandbox, the saved-query store and
// the alert store to the HTTP handlers.
type Server struct {
	reader           Reader
	sandbox          PlaygroundExecutor
	queries          QueryStore
	alerts           AlertStore
	operatorToken    string
	playgroundLimits *ipRateLimiter
	saveLimits       *ipRateLimiter
	logger           *slog.Logger
	metrics          *metrics
}

// Config wires the Server's dependencies. Sandbox, Queries and Alerts may each be
// nil to disable the corresponding endpoints. OperatorToken, when empty, locks
// the alert endpoints -- they reject every request -- so a deployment that
// forgets to set it fails closed rather than exposing operator config.
type Config struct {
	Reader        Reader
	Sandbox       PlaygroundExecutor
	Queries       QueryStore
	Alerts        AlertStore
	OperatorToken string
	Logger        *slog.Logger
	Registerer    prometheus.Registerer
}

// New constructs a Server from cfg and registers its request metrics.
func New(cfg Config) *Server {
	return &Server{
		reader:           cfg.Reader,
		sandbox:          cfg.Sandbox,
		queries:          cfg.Queries,
		alerts:           cfg.Alerts,
		operatorToken:    cfg.OperatorToken,
		playgroundLimits: newIPRateLimiter(playgroundRate, playgroundBurst, playgroundBucketTTL),
		saveLimits:       newIPRateLimiter(playgroundSaveRate, playgroundSaveBurst, playgroundBucketTTL),
		logger:           cfg.Logger,
		metrics:          newMetrics(cfg.Registerer),
	}
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
	if s.sandbox != nil {
		// The Playground alone is rate-limited per IP, since it runs untrusted SQL.
		mux.Handle("POST /api/v1/playground/query",
			s.playgroundLimits.middleware(http.HandlerFunc(s.handlePlaygroundQuery)))
	}
	if s.queries != nil {
		// Save is an open write endpoint, so it is rate-limited per IP; loading by
		// UUID is a cheap, non-enumerable lookup and is left unthrottled.
		mux.Handle("POST /api/v1/playground/save",
			s.saveLimits.middleware(http.HandlerFunc(s.handleSaveQuery)))
		mux.HandleFunc("GET /api/v1/playground/q/{id}", s.handleLoadQuery)
	}
	if s.alerts != nil {
		// Alert rules are operator configuration and hold delivery secrets (chat
		// ids, webhook URLs), so the whole resource -- reads included -- sits behind
		// the operator token, unlike the open market-data reads.
		auth := operatorAuth(s.operatorToken)
		mux.Handle("GET /api/v1/alerts", auth(http.HandlerFunc(s.handleListAlerts)))
		mux.Handle("POST /api/v1/alerts", auth(http.HandlerFunc(s.handleCreateAlert)))
		mux.Handle("DELETE /api/v1/alerts/{id}", auth(http.HandlerFunc(s.handleDeleteAlert)))
	}
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
