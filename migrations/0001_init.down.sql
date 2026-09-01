-- Migration 0001 (down): drop the initial schema.
--
-- Dropping the partitioned parent cascades to every attached partition
-- (monthly ranges and the default), returning the schema to empty.

BEGIN;

DROP TABLE IF EXISTS prices;       -- cascades to all partitions
DROP TABLE IF EXISTS instruments;

COMMIT;
