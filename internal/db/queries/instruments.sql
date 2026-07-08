-- name: UpsertInstrument :one
-- Inserts an instrument or returns the existing one's id, keyed by symbol.
INSERT INTO instruments (symbol, base_asset, quote_asset, source)
VALUES ($1, $2, $3, $4)
ON CONFLICT (symbol) DO UPDATE SET updated_at = now()
RETURNING id;
