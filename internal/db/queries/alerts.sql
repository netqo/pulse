-- name: CreateAlertRule :one
-- Persists a new alerting rule and returns the stored row.
INSERT INTO alert_rules (instrument_id, rule_type, threshold, window_seconds, channel, target)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, instrument_id, rule_type, threshold, window_seconds, channel, target, is_enabled, created_at;

-- name: ListAlertRules :many
-- Lists every alerting rule, newest first, for the management API.
SELECT id, instrument_id, rule_type, threshold, window_seconds, channel, target, is_enabled, created_at
FROM alert_rules
ORDER BY created_at DESC, id DESC;

-- name: ListEnabledAlertRules :many
-- Lists the enabled rules the Alerting service evaluates, joined to the symbol so
-- the engine can match ticks by symbol without a second lookup.
SELECT r.id, r.instrument_id, i.symbol, r.rule_type, r.threshold, r.window_seconds,
       r.channel, r.target, r.is_enabled, r.created_at
FROM alert_rules r
JOIN instruments i ON i.id = r.instrument_id
WHERE r.is_enabled
ORDER BY r.id;

-- name: DeleteAlertRule :one
-- Deletes a rule by id, returning it so the caller can tell a real deletion from
-- a missing id (no rows).
DELETE FROM alert_rules
WHERE id = $1
RETURNING id;

-- name: InsertAlertHistory :one
-- Records a fired alert and the outcome of its notification delivery.
INSERT INTO alert_history (rule_id, observed_value, delivery_status)
VALUES ($1, $2, $3)
RETURNING id, rule_id, fired_at, observed_value, delivery_status;
