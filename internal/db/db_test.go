package db

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSplitSymbol(t *testing.T) {
	tests := []struct {
		symbol, base, quote string
	}{
		{"BTCUSDT", "BTC", "USDT"},
		{"ethusdt", "ETH", "USDT"},
		{"ETHBTC", "ETH", "BTC"},
		{"BNBFDUSD", "BNB", "FDUSD"},
		{"SOLUSDC", "SOL", "USDC"},
		{"UNKNOWNX", "UNKNOWNX", ""},
	}
	for _, tc := range tests {
		base, quote := SplitSymbol(tc.symbol)
		if base != tc.base || quote != tc.quote {
			t.Errorf("SplitSymbol(%q) = (%q, %q), want (%q, %q)",
				tc.symbol, base, quote, tc.base, tc.quote)
		}
	}
}

// TestIntegration exercises the real query path against PostgreSQL. It is
// skipped unless DATABASE_URL is set (CI's Go job provides a Postgres service).
func TestIntegration(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database integration test")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	applySchema(ctx, t, pool)

	d, err := New(ctx, url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	// Upsert is idempotent by symbol.
	id1, err := d.UpsertInstrument(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("UpsertInstrument: %v", err)
	}
	id2, err := d.UpsertInstrument(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("UpsertInstrument (repeat): %v", err)
	}
	if id1 != id2 {
		t.Fatalf("UpsertInstrument returned different ids: %d vs %d", id1, id2)
	}

	monthly := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	volume := "0.00500000"
	ma := 65000.5
	vol := 12.34

	n, err := d.InsertPrices(ctx, []PriceRow{
		{InstrumentID: id1, Ts: monthly, Price: "65000.12345678", Volume: &volume, MA20: &ma, Volatility: &vol},
		{InstrumentID: id1, Ts: future, Price: "1.00000000"}, // optionals nil -> NULL, routes to default partition
	})
	if err != nil {
		t.Fatalf("InsertPrices: %v", err)
	}
	if n != 2 {
		t.Fatalf("InsertPrices inserted %d rows, want 2", n)
	}

	// The monthly row lands in its partition with the exact price preserved.
	var price, partition string
	if err := pool.QueryRow(ctx,
		`SELECT price::text, tableoid::regclass::text FROM prices WHERE instrument_id=$1 AND ts=$2`,
		id1, monthly).Scan(&price, &partition); err != nil {
		t.Fatalf("query monthly row: %v", err)
	}
	if price != "65000.12345678" {
		t.Errorf("price = %q, want 65000.12345678", price)
	}
	if partition != "prices_2025_06" {
		t.Errorf("partition = %q, want prices_2025_06", partition)
	}

	// The out-of-range row routes to the default partition with NULL optionals.
	var defaultPartition string
	var volumeIsNull bool
	if err := pool.QueryRow(ctx,
		`SELECT tableoid::regclass::text, volume IS NULL FROM prices WHERE instrument_id=$1 AND ts=$2`,
		id1, future).Scan(&defaultPartition, &volumeIsNull); err != nil {
		t.Fatalf("query default row: %v", err)
	}
	if defaultPartition != "prices_default" {
		t.Errorf("partition = %q, want prices_default", defaultPartition)
	}
	if !volumeIsNull {
		t.Error("volume should be NULL for the row with no volume")
	}
}

// TestReadPathIntegration exercises the read methods against real PostgreSQL,
// including numeric-to-string conversion, ordering, the half-open range, the row
// limit and ErrNotFound. Skipped unless DATABASE_URL is set.
func TestReadPathIntegration(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database integration test")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	applySchema(ctx, t, pool)

	d, err := New(ctx, url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	btc, err := d.UpsertInstrument(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("upsert BTC: %v", err)
	}
	eth, err := d.UpsertInstrument(ctx, "ETHUSDT")
	if err != nil {
		t.Fatalf("upsert ETH: %v", err)
	}

	t0 := time.Date(2025, 6, 10, 0, 0, 0, 0, time.UTC)
	volume := "1.5"
	ma := 100.0
	if _, err := d.InsertPrices(ctx, []PriceRow{
		{InstrumentID: btc, Ts: t0, Price: "100.00000000"},
		{InstrumentID: btc, Ts: t0.Add(time.Minute), Price: "101.00000000", Volume: &volume, MA20: &ma},
		{InstrumentID: btc, Ts: t0.Add(2 * time.Minute), Price: "102.00000000"},
		{InstrumentID: eth, Ts: t0, Price: "50.00000000"},
	}); err != nil {
		t.Fatalf("InsertPrices: %v", err)
	}

	// ListInstruments is ordered by symbol.
	insts, err := d.ListInstruments(ctx)
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	if len(insts) != 2 || insts[0].Symbol != "BTCUSDT" || insts[1].Symbol != "ETHUSDT" {
		t.Fatalf("ListInstruments = %+v, want [BTCUSDT, ETHUSDT]", insts)
	}

	// LatestPrice returns the most recent observation with exact precision; its
	// optional indicators were NULL.
	latest, err := d.LatestPrice(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("LatestPrice: %v", err)
	}
	if latest.Price != "102.00000000" {
		t.Errorf("latest price = %q, want 102.00000000", latest.Price)
	}
	if latest.Volume != nil || latest.MA20 != nil {
		t.Errorf("latest optionals = %v/%v, want nil/nil", latest.Volume, latest.MA20)
	}

	// PriceSeries returns rows oldest-first with NULLs and values preserved.
	series, err := d.PriceSeries(ctx, "BTCUSDT", t0, t0.Add(3*time.Minute), 10)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) != 3 {
		t.Fatalf("series len = %d, want 3", len(series))
	}
	if series[0].Price != "100.00000000" || series[2].Price != "102.00000000" {
		t.Errorf("series not ascending by ts: %q..%q", series[0].Price, series[2].Price)
	}
	if series[1].Volume == nil || *series[1].Volume != "1.50000000" {
		t.Errorf("series[1].Volume = %v, want 1.50000000", series[1].Volume)
	}
	if series[1].MA20 == nil || *series[1].MA20 != "100.00000000" {
		t.Errorf("series[1].MA20 = %v, want 100.00000000", series[1].MA20)
	}

	// The range is half-open: [t0, t0+2m) excludes the row exactly at t0+2m.
	half, err := d.PriceSeries(ctx, "BTCUSDT", t0, t0.Add(2*time.Minute), 10)
	if err != nil {
		t.Fatalf("PriceSeries (half-open): %v", err)
	}
	if len(half) != 2 {
		t.Errorf("half-open series len = %d, want 2", len(half))
	}

	// The limit caps the result, keeping the most recent rows in the range.
	limited, err := d.PriceSeries(ctx, "BTCUSDT", t0, t0.Add(3*time.Minute), 1)
	if err != nil {
		t.Fatalf("PriceSeries (limit): %v", err)
	}
	if len(limited) != 1 || limited[0].Price != "102.00000000" {
		t.Errorf("limited series = %+v, want single most-recent row (102)", limited)
	}

	// ClosesBefore warms an indicator window: strictly-earlier closes, oldest
	// first, capped at the limit.
	closes, err := d.ClosesBefore(ctx, btc, t0.Add(2*time.Minute), 5)
	if err != nil {
		t.Fatalf("ClosesBefore: %v", err)
	}
	if len(closes) != 2 || closes[0] != 100 || closes[1] != 101 {
		t.Errorf("ClosesBefore = %v, want [100 101] (oldest first, excludes t0+2m)", closes)
	}

	// Unknown symbols yield ErrNotFound; a known symbol with no data in range
	// yields an empty slice, not an error.
	if _, err := d.LatestPrice(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("LatestPrice(unknown) err = %v, want ErrNotFound", err)
	}
	if _, err := d.PriceSeries(ctx, "NOPE", t0, t0.Add(time.Minute), 10); !errors.Is(err, ErrNotFound) {
		t.Errorf("PriceSeries(unknown) err = %v, want ErrNotFound", err)
	}
	empty, err := d.PriceSeries(ctx, "ETHUSDT", t0.Add(time.Hour), t0.Add(2*time.Hour), 10)
	if err != nil || len(empty) != 0 {
		t.Errorf("PriceSeries(no data) = (%v, %v), want (empty, nil)", empty, err)
	}
}

// TestReplacePricesInRangeIntegration verifies the transactional range replace
// is idempotent and scoped: it clears only the target range and leaves rows
// outside it untouched. Skipped unless DATABASE_URL is set.
func TestReplacePricesInRangeIntegration(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database integration test")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	applySchema(ctx, t, pool)

	d, err := New(ctx, url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	id, err := d.UpsertInstrument(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	monthStart := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	monthEnd := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)

	// A row in the prior month must survive replacements of June.
	outside := time.Date(2024, 5, 15, 0, 0, 0, 0, time.UTC)
	if _, err := d.InsertPrices(ctx, []PriceRow{{InstrumentID: id, Ts: outside, Price: "1.00000000"}}); err != nil {
		t.Fatalf("insert outside row: %v", err)
	}

	// First seed of June: two rows.
	n1, err := d.ReplacePricesInRange(ctx, id, monthStart, monthEnd, []PriceRow{
		{InstrumentID: id, Ts: time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC), Price: "100"},
		{InstrumentID: id, Ts: time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC), Price: "200"},
	})
	if err != nil || n1 != 2 {
		t.Fatalf("first replace = (%d, %v), want (2, nil)", n1, err)
	}

	// Re-seed June with a single different row: it replaces, not appends.
	n2, err := d.ReplacePricesInRange(ctx, id, monthStart, monthEnd, []PriceRow{
		{InstrumentID: id, Ts: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC), Price: "150"},
	})
	if err != nil || n2 != 1 {
		t.Fatalf("second replace = (%d, %v), want (1, nil)", n2, err)
	}

	june, err := d.PriceSeries(ctx, "BTCUSDT", monthStart, monthEnd, 100)
	if err != nil {
		t.Fatalf("PriceSeries June: %v", err)
	}
	if len(june) != 1 || june[0].Price != "150.00000000" {
		t.Errorf("June after re-seed = %+v, want single row 150.00000000", june)
	}

	// The prior-month row is still present.
	all, err := d.PriceSeries(ctx, "BTCUSDT", time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC), monthEnd, 100)
	if err != nil {
		t.Fatalf("PriceSeries all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("total rows = %d, want 2 (outside row survived the replace)", len(all))
	}
}

// applySchema resets and applies the schema (initial tables plus the read index)
// so the integration tests run against a known, representative state.
func applySchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS prices, instruments CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	for _, path := range []string{
		"../../migrations/0001_init.up.sql",
		"../../migrations/0002_prices_instrument_ts_index.up.sql",
	} {
		up, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
}
