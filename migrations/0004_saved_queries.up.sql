-- Migration 0004 (up): Playground saved queries.
--
-- Backs the Playground "save and share" feature. A saved query is persisted here
-- and addressed by a non-enumerable UUID, so the share URL (/q/{uuid}) cannot be
-- discovered by iterating identifiers. Each row stores the SQL text and an
-- optional, opaque chart configuration that the frontend interprets (the backend
-- treats it as pass-through JSON).
--
-- The length bounds are enforced here as defense in depth, alongside the
-- application-level validation, so a misbehaving client cannot bloat the table.
-- The table is deliberately absent from the playground_readonly grant list, so
-- sandboxed user SQL cannot read back other users' saved queries.

BEGIN;

CREATE TABLE saved_queries (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    title        TEXT        CHECK (title IS NULL OR char_length(title) <= 200),
    sql_text     TEXT        NOT NULL CHECK (char_length(sql_text) BETWEEN 1 AND 20000),
    -- The application caps the raw chart_config at 32 KiB; this backstop sits
    -- generously above that (JSONB reformatting can shift the byte count), so the
    -- application check stays the binding constraint while the table is still
    -- protected against bloat if that check is ever bypassed.
    chart_config JSONB       CHECK (chart_config IS NULL OR octet_length(chart_config::text) <= 65536),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE saved_queries IS
    'Shareable Playground queries, addressed by a non-enumerable UUID.';

COMMIT;
