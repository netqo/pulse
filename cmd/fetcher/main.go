// Command fetcher connects to Binance live trade streams and produces
// normalized ticks to Kafka. It is the ingestion boundary of the pipeline.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/netqo/pulse/internal/config"
	"github.com/netqo/pulse/internal/fetcher"
	"github.com/netqo/pulse/internal/kafka"
	"github.com/netqo/pulse/internal/logging"
	"github.com/netqo/pulse/internal/version"
)

// Fetcher-specific defaults, layered on top of the shared configuration.
const (
	defaultBinanceWSURL = "wss://stream.binance.com:9443/stream"
	defaultSymbols      = "BTCUSDT,ETHUSDT"
	defaultMetricsAddr  = ":9101"
	shutdownTimeout     = 5 * time.Second
	readyTimeout        = 2 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("fetcher exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.LogLevel, cfg.IsProduction())
	logger.Info("starting fetcher", "version", version.String())

	producer, err := kafka.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopic)
	if err != nil {
		return err
	}
	defer producer.Close()

	reg := prometheus.NewRegistry()
	f := fetcher.New(
		getenv("BINANCE_WS_URL", defaultBinanceWSURL),
		splitCSV(getenv("SYMBOLS", defaultSymbols)),
		producer,
		logger,
		reg,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The fetcher is ready when it holds a live upstream connection and its
	// Kafka producer can reach the brokers.
	ready := func(ctx context.Context) error {
		if !f.Ready() {
			return errors.New("upstream stream not connected")
		}
		return producer.Ping(ctx)
	}

	srv := &http.Server{
		Addr:              getenv("METRICS_ADDR", defaultMetricsAddr),
		Handler:           newHandler(reg, ready),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if serveErr := srv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("metrics server failed", "error", serveErr)
			stop()
		}
	}()

	runErr := f.Run(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
		logger.Warn("metrics server shutdown", "error", shutdownErr)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	logger.Info("fetcher stopped")
	return nil
}

// newHandler builds the observability HTTP surface. Liveness (/healthz) reports
// that the process is up; readiness (/readyz) reflects real upstream and broker
// health via the ready function; /metrics serves the Prometheus registry.
func newHandler(reg *prometheus.Registry, ready func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		if err := ready(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeOK(w)
	})
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return mux
}

// writeOK writes a 200 OK plain-text response.
func writeOK(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// getenv returns the value of key or fallback when unset or blank.
func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

// splitCSV parses a comma-separated list, trimming and dropping empty entries.
func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
