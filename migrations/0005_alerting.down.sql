-- Migration 0005 (down): drop the alerting model.
--
-- alert_history is dropped first: it references alert_rules, so removing it before
-- the parent avoids depending on the cascade during teardown.

BEGIN;

DROP TABLE IF EXISTS alert_history;
DROP TABLE IF EXISTS alert_rules;

COMMIT;
