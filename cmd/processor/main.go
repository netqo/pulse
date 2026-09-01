// Command processor consumes normalized ticks from Kafka, enriches them with
// rolling indicators and persists the result into the partitioned prices table.
// It is the analytics stage of the pipeline.
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
	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/health"
	"github.com/netqo/pulse/internal/kafka"
	"github.com/netqo/pulse/internal/logging"
	"github.com/netqo/pulse/internal/processor"
	"github.com/netqo/pulse/internal/version"
)

// Processor-specific defaults, layered on top of the shared configuration.
const (
	consumerGroup      = "processor"
	defaultMetricsAddr = ":9102"
	defaultBatchSize   = 500
	windowSize         = 20
	shutdownTimeout    = 5 * time.Second
)

func main() {
	// The "healthcheck" subcommand backs the distroless container HEALTHCHECK.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(health.Check(config.String("METRICS_ADDR", defaultMetricsAddr)))
	}

	if err := run(); err != nil {
		slog.Error("processor exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.LogLevel, cfg.IsProduction())
	logger.Info("starting processor", "version", version.String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	consumer, err := kafka.NewConsumer(
		cfg.KafkaBrokers,
		consumerGroup,
		cfg.KafkaTopic,
		config.Int("BATCH_SIZE", defaultBatchSize),
	)
	if err != nil {
		return err
	}
	defer consumer.Close()

	reg := prometheus.NewRegistry()
	proc := processor.New(database, database, logger, reg, windowSize)

	// The processor is ready when it can reach both PostgreSQL and Kafka.
	ready := func(ctx context.Context) error {
		if err := database.Ping(ctx); err != nil {
			return err
		}
		return consumer.Ping(ctx)
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

	runErr := proc.Run(ctx, consumer)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
		logger.Warn("metrics server shutdown", "error", shutdownErr)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	logger.Info("processor stopped")
	return nil
}
