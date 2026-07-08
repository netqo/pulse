-- name: InsertPrices :copyfrom
-- Bulk-inserts enriched price rows using the COPY protocol, the fastest path
-- into the partitioned prices table.
INSERT INTO prices (instrument_id, ts, price, volume, ma_20, volatility)
VALUES ($1, $2, $3, $4, $5, $6);
