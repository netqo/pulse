// Command api serves the read side of Pulse over HTTP: instrument reference data
// and price observations. It runs two listeners, a public API surface and a
// separate internal observability surface (health and metrics).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/api"
	"github.com/netqo/pulse/internal/config"
	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/health"
	"github.com/netqo/pulse/internal/logging"
	"github.com/netqo/pulse/internal/playground"
	"github.com/netqo/pulse/internal/version"
)

// API-specific defaults, layered on top of the shared configuration.
const (
	defaultAPIAddr     = ":8080"
	defaultMetricsAddr = ":9103"
	readHeaderTimeout  = 5 * time.Second
	shutdownTimeout    = 5 * time.Second
	// sandboxMaxConns bounds the Playground's dedicated pool so untrusted SQL
	// (e.g. a burst of pg_sleep) cannot starve the connections serving the rest
	// of the API.
	sandboxMaxConns = 4
)

func main() {
	// The "healthcheck" subcommand backs the distroless container HEALTHCHECK,
	// probing the internal observability listener.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(health.Check(config.String("METRICS_ADDR", defaultMetricsAddr)))
	}

	if err := run(); err != nil {
		slog.Error("api exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.LogLevel, cfg.IsProduction())
	logger.Info("starting api", "version", version.String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()

	// The Playground runs untrusted SQL on its own bounded pool, isolated from
	// the pool serving the rest of the API.
	sandboxPool, err := db.NewPool(ctx, cfg.DatabaseURL, sandboxMaxConns)
	if err != nil {
		return err
	}
	defer sandboxPool.Close()

	reg := prometheus.NewRegistry()
	sandbox := playground.New(sandboxPool)
	server := api.New(database, sandbox, logger, reg)

	ready := func(ctx context.Context) error { return database.Ping(ctx) }

	apiSrv := &http.Server{
		Addr:              config.String("API_ADDR", defaultAPIAddr),
		Handler:           server.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	obsSrv := &http.Server{
		Addr:              config.String("METRICS_ADDR", defaultMetricsAddr),
		Handler:           health.Handler(reg, ready),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	servers := []struct {
		srv  *http.Server
		name string
	}{
		{apiSrv, "api"},
		{obsSrv, "metrics"},
	}

	// A buffered slot per server lets a failing listener report its error
	// without blocking, so a bind failure surfaces as a non-zero exit rather
	// than being mistaken for a clean shutdown.
	serveErr := make(chan error, len(servers))
	for _, s := range servers {
		go func(srv *http.Server, name string) {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("%s server: %w", name, err)
			}
		}(s.srv, s.name)
	}

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serveErr:
		logger.Error("server failed, shutting down", "error", runErr)
	}

	// Shut both servers down concurrently so each gets the full grace period.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func(srv *http.Server, name string) {
			defer wg.Done()
			if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
				logger.Warn(name+" server shutdown", "error", shutdownErr)
			}
		}(s.srv, s.name)
	}
	wg.Wait()

	if runErr != nil {
		return runErr
	}
	logger.Info("api stopped")
	return nil
}
