// Package db is the PostgreSQL access layer for Pulse. It wraps the
// sqlc-generated queries with a pgx connection pool and domain-friendly helpers
// so services work with plain Go types instead of pgx wire types.
package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/netqo/pulse/internal/db/sqlc"
)

const instrumentSource = "binance"

// ErrNotFound is returned by read methods when the requested instrument (or the
// data it would expose) does not exist, letting callers map it to a 404.
var ErrNotFound = errors.New("db: not found")

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

// Instrument is reference data for a tradable symbol, as returned to read
// callers without exposing the underlying pgx wire types.
type Instrument struct {
	ID         int64
	Symbol     string
	BaseAsset  string
	QuoteAsset string
	Source     string
	IsActive   bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// PricePoint is a single price observation with its derived indicators. Decimal
// fields are decimal strings to preserve the stored NUMERIC precision exactly;
// the optional ones are nil when the column is NULL.
type PricePoint struct {
	Ts         time.Time
	Price      string
	Volume     *string
	MA20       *string
	Volatility *string
}

// ListInstruments returns every instrument, ordered by symbol.
func (d *DB) ListInstruments(ctx context.Context) ([]Instrument, error) {
	rows, err := d.queries.ListInstruments(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: list instruments: %w", err)
	}
	out := make([]Instrument, 0, len(rows))
	for _, r := range rows {
		out = append(out, Instrument{
			ID:         r.ID,
			Symbol:     r.Symbol,
			BaseAsset:  r.BaseAsset,
			QuoteAsset: r.QuoteAsset,
			Source:     r.Source,
			IsActive:   r.IsActive,
			CreatedAt:  r.CreatedAt.Time,
			UpdatedAt:  r.UpdatedAt.Time,
		})
	}
	return out, nil
}

// LatestPrice returns the most recent price observation for symbol. It returns
// ErrNotFound when the symbol is unknown or has no recorded prices yet.
func (d *DB) LatestPrice(ctx context.Context, symbol string) (PricePoint, error) {
	id, err := d.instrumentIDBySymbol(ctx, symbol)
	if err != nil {
		return PricePoint{}, err
	}
	row, err := d.queries.GetLatestPrice(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PricePoint{}, ErrNotFound
		}
		return PricePoint{}, fmt.Errorf("db: latest price %q: %w", symbol, err)
	}
	return toPricePoint(row.Ts, row.Price, row.Volume, row.Ma20, row.Volatility)
}

// PriceSeries returns symbol's price observations within the half-open range
// [from, to), oldest first. When more rows exist than limit, the most recent
// ones in the range are kept. It returns ErrNotFound when the symbol is unknown;
// a known symbol with no data in range yields an empty slice.
func (d *DB) PriceSeries(ctx context.Context, symbol string, from, to time.Time, limit int) ([]PricePoint, error) {
	id, err := d.instrumentIDBySymbol(ctx, symbol)
	if err != nil {
		return nil, err
	}
	rows, err := d.queries.GetPriceSeries(ctx, sqlc.GetPriceSeriesParams{
		InstrumentID: id,
		FromTs:       pgtype.Timestamptz{Time: from.UTC(), Valid: true},
		ToTs:         pgtype.Timestamptz{Time: to.UTC(), Valid: true},
		RowLimit:     int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("db: price series %q: %w", symbol, err)
	}
	points := make([]PricePoint, 0, len(rows))
	for _, r := range rows {
		p, err := toPricePoint(r.Ts, r.Price, r.Volume, r.Ma20, r.Volatility)
		if err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, nil
}

// instrumentIDBySymbol resolves a symbol to its instrument id, translating a
// missing row into ErrNotFound. Read methods resolve the id in this separate
// round trip (rather than a JOIN) on purpose: it keeps "unknown symbol" (404)
// distinct from "known symbol, no data" (empty result), which a JOIN would blur.
func (d *DB) instrumentIDBySymbol(ctx context.Context, symbol string) (int64, error) {
	inst, err := d.queries.GetInstrumentBySymbol(ctx, symbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("db: get instrument %q: %w", symbol, err)
	}
	return inst.ID, nil
}

// toPricePoint converts the pgx wire fields of a price row into a PricePoint.
func toPricePoint(ts pgtype.Timestamptz, price, volume, ma20, volatility pgtype.Numeric) (PricePoint, error) {
	p, ok := numericToString(price)
	if !ok {
		return PricePoint{}, errors.New("db: price is null or invalid")
	}
	return PricePoint{
		Ts:         ts.Time,
		Price:      p,
		Volume:     optNumericToString(volume),
		MA20:       optNumericToString(ma20),
		Volatility: optNumericToString(volatility),
	}, nil
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

// numericToString renders a pgtype.Numeric as its exact decimal string,
// reporting false when the value is NULL. pgtype.Numeric.Value encodes a valid
// numeric to its canonical text form, preserving the stored precision.
func numericToString(n pgtype.Numeric) (string, bool) {
	if !n.Valid {
		return "", false
	}
	v, err := n.Value()
	if err != nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

// optNumericToString renders an optional numeric, yielding nil for a NULL value.
// A NULL and an (unexpected) encode failure both map to nil; callers use this
// only for the nullable indicator columns, where absence is the correct default.
func optNumericToString(n pgtype.Numeric) *string {
	if s, ok := numericToString(n); ok {
		return &s
	}
	return nil
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
