-- Migration 0003 (up): the least-privilege Playground role.
--
-- The SQL Playground executes arbitrary user SQL, so it is sandboxed in layers.
-- This migration provides the role layer: a dedicated, least-privilege role the
-- API assumes (via SET LOCAL ROLE) for the duration of each read-only, timeout-
-- bounded query transaction. The role has SELECT on the whitelisted tables and
-- nothing else -- no DDL, no DML, no access to any other object.
--
-- The role is NOLOGIN: it is never used to open a connection directly, so the
-- sandbox needs no separate credential. The application role is granted
-- membership so it can assume the role at query time.

BEGIN;

-- Roles are cluster-global, so create idempotently (there is no CREATE ROLE IF
-- NOT EXISTS).
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'playground_readonly') THEN
        CREATE ROLE playground_readonly NOLOGIN;
    END IF;
END $$;

-- Whitelist: SELECT only, on the reference and price data. Querying the
-- partitioned prices parent checks privileges on the parent alone, so this
-- covers every current and future monthly partition.
GRANT SELECT ON instruments, prices TO playground_readonly;

-- Let the application role assume the sandbox role for a single transaction.
GRANT playground_readonly TO CURRENT_USER;

COMMIT;
