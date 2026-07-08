package db

import (
	"context"
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

// applySchema resets and applies the initial schema so the integration test runs
// against a known state.
func applySchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS prices, instruments CASCADE`); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	up, err := os.ReadFile("../../migrations/0001_init.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}
