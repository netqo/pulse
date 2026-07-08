-- Migration 0003 (down): drop the Playground role and its grants.

BEGIN;

REVOKE playground_readonly FROM CURRENT_USER;
-- Remove every privilege granted to the role in this database so the role can
-- be dropped cleanly.
DROP OWNED BY playground_readonly;
DROP ROLE IF EXISTS playground_readonly;

COMMIT;
