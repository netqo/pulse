-- name: InsertPrices :copyfrom
-- Bulk-inserts enriched price rows using the COPY protocol, the fastest path
-- into the partitioned prices table.
INSERT INTO prices (instrument_id, ts, price, volume, ma_20, volatility)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetClosesBefore :many
-- Returns up to limit price values strictly before a timestamp for an
-- instrument, most recent first. Warms the seeder's rolling window so the
-- derived indicators stay continuous regardless of which range a run seeds.
-- Backed by the (instrument_id, ts DESC) index.
SELECT price
FROM prices
WHERE instrument_id = $1
  AND ts < sqlc.arg(before_ts)
ORDER BY ts DESC
LIMIT sqlc.arg(row_limit);

-- name: DeletePricesInRange :execrows
-- Deletes an instrument's price rows within the half-open range [from, to).
-- Backs the historical seeder's idempotent per-window replace.
DELETE FROM prices
WHERE instrument_id = $1
  AND ts >= sqlc.arg(from_ts)
  AND ts < sqlc.arg(to_ts);

-- name: GetLatestPrice :one
-- Returns the most recent price observation for an instrument. Backed by the
-- (instrument_id, ts DESC) index, which is walked backwards without a sort.
SELECT ts, price, volume, ma_20, volatility
FROM prices
WHERE instrument_id = $1
ORDER BY ts DESC
LIMIT 1;

-- name: GetPriceSeries :many
-- Returns an instrument's price observations within the half-open range
-- [from, to), returned oldest first. When more rows exist than the caller's
-- limit, the most RECENT ones are kept (the inner query walks the
-- (instrument_id, ts DESC) index backwards and takes the newest limit rows),
-- then the outer query re-sorts them ascending for the response.
SELECT ts, price, volume, ma_20, volatility
FROM (
    SELECT ts, price, volume, ma_20, volatility
    FROM prices
    WHERE instrument_id = $1
      AND ts >= sqlc.arg(from_ts)
      AND ts < sqlc.arg(to_ts)
    ORDER BY ts DESC
    LIMIT sqlc.arg(row_limit)
) recent
ORDER BY ts;
