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

	srv := &http.Server{
		Addr:              getenv("METRICS_ADDR", defaultMetricsAddr),
		Handler:           newHandler(reg),
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

// newHandler builds the observability HTTP surface: liveness, readiness and
// the Prometheus metrics endpoint.
func newHandler(reg *prometheus.Registry) http.Handler {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	mux.HandleFunc("/healthz", ok)
	mux.HandleFunc("/readyz", ok)
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return mux
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
