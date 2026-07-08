-- Migration 0004 (down): drop the Playground saved queries table.

BEGIN;

DROP TABLE IF EXISTS saved_queries;

COMMIT;
