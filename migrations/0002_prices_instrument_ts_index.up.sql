-- Migration 0002 (up): the (instrument_id, ts DESC) read index on prices.
--
-- This is the platform's first justified secondary index, deferred from 0001 so
-- the performance story can be shown both without and with it. Measured with
-- EXPLAIN (ANALYZE, BUFFERS) over 3M rows across 10 instruments and two monthly
-- partitions (full plans captured in the pull request):
--
--   * "latest price for an instrument"
--     (WHERE instrument_id = X ORDER BY ts DESC LIMIT 1):
--       before -> 244 ms, Parallel Append seq-scanning every partition
--       after  -> 1.2 ms, Merge Append of per-partition index scans (~200x)
--
--   * "historical series for an instrument over a time range"
--     (WHERE instrument_id = X AND ts >= a AND ts < b ORDER BY ts):
--       before -> 150 ms, full seq scan of the month partition
--       after  -> 96 ms, Bitmap Index Scan; the gain widens as the selected
--       fraction of the partition shrinks.
--
-- ts is indexed DESC so the "latest" query walks the index backwards and skips
-- the sort entirely. Creating the index on the partitioned parent makes
-- PostgreSQL propagate a matching local index to every existing and future
-- partition. A plain (non-CONCURRENT) build is used because migrations run at
-- deploy time inside a transaction; a zero-downtime rollout on a hot table would
-- instead build each partition's index CONCURRENTLY and ATTACH it.

BEGIN;

CREATE INDEX idx_prices_instrument_ts ON prices (instrument_id, ts DESC);

COMMIT;
