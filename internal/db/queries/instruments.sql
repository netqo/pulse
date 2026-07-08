-- name: UpsertInstrument :one
-- Inserts an instrument or returns the existing one's id, keyed by symbol.
INSERT INTO instruments (symbol, base_asset, quote_asset, source)
VALUES ($1, $2, $3, $4)
ON CONFLICT (symbol) DO UPDATE SET updated_at = now()
RETURNING id;

-- name: GetInstrumentBySymbol :one
-- Returns the full instrument row for a symbol, or no rows if it is unknown.
SELECT id, symbol, base_asset, quote_asset, source, is_active, created_at, updated_at
FROM instruments
WHERE symbol = $1;

-- name: ListInstruments :many
-- Lists every instrument, ordered by symbol for a stable response.
SELECT id, symbol, base_asset, quote_asset, source, is_active, created_at, updated_at
FROM instruments
ORDER BY symbol;
