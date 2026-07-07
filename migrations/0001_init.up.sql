-- Migration 0001 (up): initial schema.
--
-- Establishes the two foundational tables of the platform:
--   * instruments - reference data for tradable symbols.
--   * prices      - the append-heavy, time-series fact table, range-partitioned
--                   by month from day one (the dominant access pattern is
--                   "symbol X over time range T").
--
-- The justified (instrument_id, ts DESC) secondary index is deliberately NOT
-- created here: it is introduced in Phase 1 alongside its EXPLAIN ANALYZE
-- before/after benchmark, so the performance story can show the query both
-- without and with the index.

BEGIN;

CREATE TABLE instruments (
    id          BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    symbol      TEXT        NOT NULL UNIQUE,          -- e.g. 'BTCUSDT'
    base_asset  TEXT        NOT NULL,                 -- 'BTC'
    quote_asset TEXT        NOT NULL,                 -- 'USDT'
    source      TEXT        NOT NULL,                 -- 'binance' | 'coingecko'
    is_active   BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE prices (
    instrument_id BIGINT        NOT NULL REFERENCES instruments (id),
    ts            TIMESTAMPTZ   NOT NULL,             -- event time (exchange timestamp)
    price         NUMERIC(20, 8) NOT NULL,
    volume        NUMERIC(20, 8),
    ma_20         NUMERIC(20, 8),                     -- 20-period moving average (Processor)
    volatility    NUMERIC(20, 8),                     -- rolling stddev (Processor)
    ingested_at   TIMESTAMPTZ   NOT NULL DEFAULT now() -- pipeline arrival time
) PARTITION BY RANGE (ts);

-- Safety-net partition: captures any timestamp outside the explicit monthly
-- ranges so an insert never fails for lack of a partition. Automated monthly
-- partition management (a pre-create job or pg_partman) arrives later per the
-- roadmap; the default partition keeps the system correct until then.
CREATE TABLE prices_default PARTITION OF prices DEFAULT;

-- Pre-create monthly partitions across the project's active window.
DO $$
DECLARE
    start_month CONSTANT date := date '2024-01-01';
    end_month   CONSTANT date := date '2027-01-01';
    m           date := start_month;
    part_name   text;
BEGIN
    WHILE m < end_month LOOP
        part_name := format('prices_%s', to_char(m, 'YYYY_MM'));
        EXECUTE format(
            'CREATE TABLE %I PARTITION OF prices FOR VALUES FROM (%L) TO (%L);',
            part_name, m, (m + interval '1 month')::date
        );
        m := (m + interval '1 month')::date;
    END LOOP;
END $$;

COMMIT;
