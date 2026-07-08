// Command fetcher connects to Binance live trade streams and produces
// normalized ticks to Kafka. It is the ingestion boundary of the pipeline.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/config"
	"github.com/netqo/pulse/internal/fetcher"
	"github.com/netqo/pulse/internal/health"
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
	// The "healthcheck" subcommand probes the local liveness endpoint and exits
	// with a status code. It backs the container HEALTHCHECK, which cannot use a
	// shell because the runtime image is distroless.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(health.Check(config.String("METRICS_ADDR", defaultMetricsAddr)))
	}

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
		config.String("BINANCE_WS_URL", defaultBinanceWSURL),
		config.CSV("SYMBOLS", defaultSymbols),
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
		Addr:              config.String("METRICS_ADDR", defaultMetricsAddr),
		Handler:           health.Handler(reg, ready),
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
