// Package db is the PostgreSQL access layer for Pulse. It wraps the
// sqlc-generated queries with a pgx connection pool and domain-friendly helpers
// so services work with plain Go types instead of pgx wire types.
package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netqo/pulse/internal/db/sqlc"
)

const instrumentSource = "binance"

// DB is the application's handle to PostgreSQL.
type DB struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

// New opens a connection pool to databaseURL and verifies connectivity.
func New(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return &DB{pool: pool, queries: sqlc.New(pool)}, nil
}

// Ping verifies connectivity to the database.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("db: ping: %w", err)
	}
	return nil
}

// Close releases the connection pool.
func (d *DB) Close() {
	d.pool.Close()
}

// UpsertInstrument ensures an instrument row exists for symbol and returns its
// id. The base and quote assets are derived from the symbol.
func (d *DB) UpsertInstrument(ctx context.Context, symbol string) (int64, error) {
	base, quote := SplitSymbol(symbol)
	id, err := d.queries.UpsertInstrument(ctx, sqlc.UpsertInstrumentParams{
		Symbol:     symbol,
		BaseAsset:  base,
		QuoteAsset: quote,
		Source:     instrumentSource,
	})
	if err != nil {
		return 0, fmt.Errorf("db: upsert instrument %q: %w", symbol, err)
	}
	return id, nil
}

// PriceRow is a single enriched price observation to persist. Price is required;
// Volume, MA20 and Volatility are optional and stored as NULL when nil.
type PriceRow struct {
	InstrumentID int64
	Ts           time.Time
	Price        string
	Volume       *string
	MA20         *float64
	Volatility   *float64
}

// InsertPrices bulk-loads rows into the partitioned prices table via the COPY
// protocol, returning the number of rows inserted.
func (d *DB) InsertPrices(ctx context.Context, rows []PriceRow) (int64, error) {
	params := make([]sqlc.InsertPricesParams, 0, len(rows))
	for i, r := range rows {
		price, err := decimalNumeric(r.Price)
		if err != nil {
			return 0, fmt.Errorf("db: row %d price %q: %w", i, r.Price, err)
		}
		volume, err := optDecimalNumeric(r.Volume)
		if err != nil {
			return 0, fmt.Errorf("db: row %d volume: %w", i, err)
		}
		params = append(params, sqlc.InsertPricesParams{
			InstrumentID: r.InstrumentID,
			Ts:           pgtype.Timestamptz{Time: r.Ts.UTC(), Valid: true},
			Price:        price,
			Volume:       volume,
			Ma20:         floatNumeric(r.MA20),
			Volatility:   floatNumeric(r.Volatility),
		})
	}

	n, err := d.queries.InsertPrices(ctx, params)
	if err != nil {
		return 0, fmt.Errorf("db: insert prices: %w", err)
	}
	return n, nil
}

// knownQuoteAssets lists recognized quote currencies, longest first so a longer
// suffix wins over a shorter one when both would match.
var knownQuoteAssets = []string{
	"FDUSD",
	"USDT", "USDC", "TUSD", "BUSD",
	"DAI", "EUR", "TRY", "BRL", "GBP", "BTC", "ETH", "BNB", "SOL", "XRP",
}

// SplitSymbol splits a trading symbol into base and quote assets using the known
// quote-currency list. Unknown symbols return the whole symbol as the base with
// an empty quote.
func SplitSymbol(symbol string) (base, quote string) {
	upper := strings.ToUpper(strings.TrimSpace(symbol))
	for _, q := range knownQuoteAssets {
		if len(upper) > len(q) && strings.HasSuffix(upper, q) {
			return upper[:len(upper)-len(q)], q
		}
	}
	return upper, ""
}

// decimalNumeric parses a required decimal string into a pgtype.Numeric.
func decimalNumeric(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return n, nil
}

// optDecimalNumeric parses an optional decimal string, yielding a NULL numeric
// when the pointer is nil.
func optDecimalNumeric(s *string) (pgtype.Numeric, error) {
	if s == nil {
		return pgtype.Numeric{}, nil
	}
	return decimalNumeric(*s)
}

// floatNumeric converts an optional float into a pgtype.Numeric, yielding a NULL
// numeric when the pointer is nil.
func floatNumeric(f *float64) pgtype.Numeric {
	if f == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	// Formatting through a decimal string preserves the value for the NUMERIC
	// column without float-to-numeric surprises.
	if err := n.Scan(strconv.FormatFloat(*f, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}
	}
	return n
}
