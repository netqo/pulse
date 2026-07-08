// Package seed backfills the prices table with historical market data from the
// public data.binance.vision archives. It downloads monthly kline archives,
// derives the same rolling indicators the live processor computes, and writes
// the enriched rows into the partitioned prices table, one month at a time and
// idempotently, so a re-run replaces rather than duplicates a window.
package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/indicators"
)

// ErrNoData signals that an archive has no data for a requested month (a 404),
// which the seeder treats as a skippable gap rather than a failure.
var ErrNoData = errors.New("seed: no data for month")

// Kline is the subset of an archived candlestick the seeder persists.
type Kline struct {
	OpenTime time.Time
	Close    string // decimal string, preserved for the NUMERIC column
	Volume   string // decimal string
}

// Fetcher retrieves one symbol-month of klines from an archive, oldest first,
// returning ErrNoData when the month is absent.
type Fetcher interface {
	FetchMonth(ctx context.Context, symbol, interval string, month time.Time) ([]Kline, error)
}

// Store is the data access the seeder depends on. *db.DB satisfies it.
type Store interface {
	UpsertInstrument(ctx context.Context, symbol string) (int64, error)
	ReplacePricesInRange(ctx context.Context, instrumentID int64, from, to time.Time, rows []db.PriceRow) (int64, error)
	// ClosesBefore returns up to limit prior closes, oldest first, used to warm
	// the rolling window so indicators are continuous across separately-seeded
	// ranges.
	ClosesBefore(ctx context.Context, instrumentID int64, before time.Time, limit int) ([]float64, error)
}

// Seeder backfills the prices table from archived klines.
type Seeder struct {
	fetcher    Fetcher
	store      Store
	logger     *slog.Logger
	interval   string
	windowSize int
}

// New constructs a Seeder that reads interval klines and computes indicators
// over a windowSize-length window (matching the live processor's ma_20).
func New(fetcher Fetcher, store Store, logger *slog.Logger, interval string, windowSize int) *Seeder {
	return &Seeder{
		fetcher:    fetcher,
		store:      store,
		logger:     logger,
		interval:   interval,
		windowSize: windowSize,
	}
}

// Summary reports what a Seed run wrote.
type Summary struct {
	Symbols int   // symbols processed
	Months  int   // symbol-months with an archive present
	Rows    int64 // total rows written
}

// Seed backfills every month in the inclusive range [from, to] for each symbol.
// It processes months in order so the rolling indicators stay continuous across
// month boundaries, and stops at the first hard error. The window is warmed from
// the closes already stored just before from, so the derived indicators are
// deterministic regardless of which range a run seeds.
func (s *Seeder) Seed(ctx context.Context, symbols []string, from, to time.Time) (Summary, error) {
	var summary Summary
	first := firstOfMonth(from)
	last := firstOfMonth(to)

	for _, symbol := range symbols {
		id, err := s.store.UpsertInstrument(ctx, symbol)
		if err != nil {
			return summary, fmt.Errorf("seed: upsert %q: %w", symbol, err)
		}
		summary.Symbols++

		// One window per symbol, warmed from prior stored closes and then
		// carried across months for continuity.
		window := indicators.NewWindow(s.windowSize)
		warm, err := s.store.ClosesBefore(ctx, id, first, s.windowSize-1)
		if err != nil {
			return summary, fmt.Errorf("seed: warm window %q: %w", symbol, err)
		}
		for _, price := range warm {
			window.Add(price)
		}

		for month := first; !month.After(last); month = month.AddDate(0, 1, 0) {
			n, present, err := s.seedMonth(ctx, symbol, id, month, window)
			if err != nil {
				return summary, err
			}
			if present {
				summary.Months++
				summary.Rows += n
			}
		}
	}
	return summary, nil
}

// seedMonth fetches, enriches and idempotently writes a single symbol-month. The
// bool reports whether an archive existed for the month.
func (s *Seeder) seedMonth(ctx context.Context, symbol string, id int64, month time.Time, window *indicators.Window) (int64, bool, error) {
	label := month.Format("2006-01")

	klines, err := s.fetcher.FetchMonth(ctx, symbol, s.interval, month)
	if errors.Is(err, ErrNoData) {
		s.logger.Warn("no archive for month, skipping", "symbol", symbol, "month", label)
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("seed: fetch %s %s: %w", symbol, label, err)
	}

	rows := make([]db.PriceRow, 0, len(klines))
	for _, k := range klines {
		price, err := strconv.ParseFloat(k.Close, 64)
		if err != nil {
			return 0, false, fmt.Errorf("seed: %s %s: parse close %q: %w", symbol, label, k.Close, err)
		}
		window.Add(price)

		volume := k.Volume
		row := db.PriceRow{InstrumentID: id, Ts: k.OpenTime, Price: k.Close, Volume: &volume}
		if ma, ok := window.Mean(); ok {
			row.MA20 = &ma
		}
		if vol, ok := window.Volatility(); ok {
			row.Volatility = &vol
		}
		rows = append(rows, row)
	}

	monthStart := firstOfMonth(month)
	monthEnd := monthStart.AddDate(0, 1, 0)
	n, err := s.store.ReplacePricesInRange(ctx, id, monthStart, monthEnd, rows)
	if err != nil {
		return 0, false, fmt.Errorf("seed: write %s %s: %w", symbol, label, err)
	}
	s.logger.Info("seeded month", "symbol", symbol, "month", label, "rows", n)
	return n, true, nil
}

// firstOfMonth truncates t to midnight UTC on the first of its month.
func firstOfMonth(t time.Time) time.Time {
	y, m, _ := t.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}
