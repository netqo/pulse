-- Migration 0002 (down): drop the (instrument_id, ts DESC) read index.
-- Dropping the partitioned parent index removes every propagated child index.

BEGIN;

DROP INDEX IF EXISTS idx_prices_instrument_ts;

COMMIT;
