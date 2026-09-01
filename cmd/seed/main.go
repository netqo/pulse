// Command seed backfills the prices table with historical klines from the
// data.binance.vision archives. It is a one-shot batch job, not a long-running
// service: it seeds the requested symbols and month range, then exits.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/netqo/pulse/internal/config"
	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/logging"
	"github.com/netqo/pulse/internal/seed"
	"github.com/netqo/pulse/internal/version"
)

const (
	monthLayout        = "2006-01"
	windowSize         = 20
	defaultInterval    = "1m"
	defaultSymbols     = "BTCUSDT,ETHUSDT"
	defaultHTTPTimeout = 60 * time.Second
)

// options holds the parsed command-line configuration.
type options struct {
	symbols  []string
	from     time.Time
	to       time.Time
	interval string
	baseURL  string
	timeout  time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// The flag package already printed usage; -h/-help is not a failure.
			return
		}
		slog.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}

	logger := logging.New(config.Level(), config.String("APP_ENV", "") == config.EnvProduction)
	logger.Info("starting seed", "version", version.String(),
		"symbols", opts.symbols, "from", opts.from.Format(monthLayout), "to", opts.to.Format(monthLayout))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The seed is a batch backfill: it needs only the database, so it reads
	// DATABASE_URL directly rather than through config.Load, which also requires
	// and validates the Kafka and Redis settings the seed never uses. db.New
	// validates connectivity, surfacing a malformed URL as a startup error.
	database, err := db.New(ctx, config.String("DATABASE_URL", config.DefaultDatabaseURL))
	if err != nil {
		return err
	}
	defer database.Close()

	fetcher := seed.NewHTTPFetcher(opts.baseURL, opts.timeout)
	seeder := seed.New(fetcher, database, logger, opts.interval, windowSize)

	summary, err := seeder.Seed(ctx, opts.symbols, opts.from, opts.to)
	if err != nil {
		return err
	}

	logger.Info("seed complete",
		"symbols", summary.Symbols, "months", summary.Months, "rows", summary.Rows)
	return nil
}

// parseFlags parses and validates the command-line options.
func parseFlags(args []string) (options, error) {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	var (
		symbols  = fs.String("symbols", defaultSymbols, "comma-separated symbols to seed")
		fromStr  = fs.String("from", "", "first month to seed, inclusive (YYYY-MM)")
		toStr    = fs.String("to", "", "last month to seed, inclusive (YYYY-MM)")
		interval = fs.String("interval", defaultInterval, "kline interval (e.g. 1m, 5m, 1h)")
		baseURL  = fs.String("base-url", seed.DefaultBaseURL, "archive base URL")
		timeout  = fs.Duration("http-timeout", defaultHTTPTimeout, "per-request download timeout")
	)
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	from, err := parseMonth(*fromStr)
	if err != nil {
		return options{}, fmt.Errorf("invalid -from: %w", err)
	}
	to, err := parseMonth(*toStr)
	if err != nil {
		return options{}, fmt.Errorf("invalid -to: %w", err)
	}
	if to.Before(from) {
		return options{}, errors.New("-to must not be before -from")
	}

	syms := splitSymbols(*symbols)
	if len(syms) == 0 {
		return options{}, errors.New("-symbols must list at least one symbol")
	}

	return options{
		symbols:  syms,
		from:     from,
		to:       to,
		interval: strings.TrimSpace(*interval),
		baseURL:  strings.TrimSpace(*baseURL),
		timeout:  *timeout,
	}, nil
}

// parseMonth parses a required YYYY-MM value into the first of that month (UTC).
func parseMonth(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("month is required (YYYY-MM)")
	}
	return time.Parse(monthLayout, raw)
}

// splitSymbols parses a comma-separated symbol list, upper-casing and dropping
// blanks.
func splitSymbols(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.ToUpper(strings.TrimSpace(p)); s != "" {
			out = append(out, s)
		}
	}
	return out
}
