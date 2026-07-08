// Command fetcher connects to Binance live trade streams and produces
// normalized ticks to Kafka. It is the ingestion boundary of the pipeline.
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	// The "healthcheck" subcommand probes the local liveness endpoint and exits
	// with a status code. It backs the container HEALTHCHECK, which cannot use a
	// shell because the runtime image is distroless.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
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

// healthcheck probes the local liveness endpoint and returns a process exit
// code (0 healthy, 1 unhealthy).
func healthcheck() int {
	url, err := healthURL(config.String("METRICS_ADDR", defaultMetricsAddr))
	if err != nil {
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()
	return probe(ctx, url)
}

// probe issues a GET and returns 0 only on a 200 OK response.
func probe(ctx context.Context, url string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// healthURL derives the liveness URL to probe from a listen address such as
// ":9101" or "0.0.0.0:9101", targeting the loopback interface.
func healthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
