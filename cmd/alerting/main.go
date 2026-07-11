// Command alerting consumes normalized ticks from Kafka on its own consumer
// group, evaluates user-configured alert rules against each tick, and dispatches
// notifications (Telegram, Discord, generic webhook) while recording an audit
// history. It is the rule-evaluation stage of the pipeline.
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

	"github.com/netqo/pulse/internal/alerting"
	"github.com/netqo/pulse/internal/config"
	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/health"
	"github.com/netqo/pulse/internal/kafka"
	"github.com/netqo/pulse/internal/logging"
	"github.com/netqo/pulse/internal/notify"
	"github.com/netqo/pulse/internal/version"
)

// Alerting-specific defaults, layered on top of the shared configuration.
const (
	consumerGroup         = "alerting"
	defaultMetricsAddr    = ":9104"
	defaultBatchSize      = 500
	defaultRefreshSeconds = 30
	notifyTimeout         = 10 * time.Second
	shutdownTimeout       = 5 * time.Second
)

func main() {
	// The "healthcheck" subcommand backs the distroless container HEALTHCHECK.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(health.Check(config.String("METRICS_ADDR", defaultMetricsAddr)))
	}

	if err := run(); err != nil {
		slog.Error("alerting exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.LogLevel, cfg.IsProduction())
	logger.Info("starting alerting", "version", version.String())

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

	dispatcher := buildDispatcher(logger)
	refresh := time.Duration(config.Int("RULE_REFRESH_SECONDS", defaultRefreshSeconds)) * time.Second

	reg := prometheus.NewRegistry()
	svc := alerting.New(database, database, dispatcher, logger, reg, refresh)

	// The service is ready when it can reach both PostgreSQL and Kafka.
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

	runErr := svc.Run(ctx, consumer)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
		logger.Warn("metrics server shutdown", "error", shutdownErr)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	logger.Info("alerting stopped")
	return nil
}

// buildDispatcher wires the notification channels from configuration. Discord and
// generic webhooks need no server-side credential (the target URL carries it), so
// they are always available; Telegram is registered only when a bot token is set.
func buildDispatcher(logger *slog.Logger) *notify.Dispatcher {
	client := &http.Client{Timeout: notifyTimeout}
	senders := map[string]notify.Sender{
		db.ChannelDiscord: notify.NewDiscordSender(client),
		db.ChannelWebhook: notify.NewWebhookSender(client),
	}
	if token := config.String("TELEGRAM_BOT_TOKEN", ""); token != "" {
		senders[db.ChannelTelegram] = notify.NewTelegramSender(client, token)
	} else {
		logger.Warn("TELEGRAM_BOT_TOKEN not set; telegram alerts will fail to deliver")
	}
	return notify.NewDispatcher(senders)
}
