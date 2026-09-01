-- Migration 0005 (up): the alerting model -- rules and fired-alert history.
--
-- Backs Phase 3. alert_rules holds the user-configured conditions the independent
-- Alerting service evaluates against the live tick stream; alert_history is the
-- durable audit log of every rule that fired and how its notification was
-- delivered.
--
-- Sandbox note: neither table is granted to playground_readonly, so arbitrary
-- user SQL in the Playground cannot read alert_rules.target (which holds delivery
-- secrets such as Telegram chat ids and webhook URLs) or the alert history. The
-- whitelist in migration 0003 deliberately covers only instruments and prices.

BEGIN;

CREATE TABLE alert_rules (
    id             BIGINT         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    instrument_id  BIGINT         NOT NULL REFERENCES instruments(id),
    rule_type      TEXT           NOT NULL
                       CHECK (rule_type IN ('price_below', 'price_above', 'change_pct', 'crosses')),
    threshold      NUMERIC(20, 8) NOT NULL,
    -- Positive when present. Meaningful only to windowed rules; the
    -- window-matches-type constraint below ties it to change_pct exactly.
    window_seconds INTEGER        CHECK (window_seconds IS NULL OR window_seconds > 0),
    channel        TEXT           NOT NULL CHECK (channel IN ('telegram', 'discord', 'webhook')),
    target         TEXT           NOT NULL CHECK (char_length(target) BETWEEN 1 AND 2000),
    is_enabled     BOOLEAN        NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT now(),
    -- A change_pct rule needs a window and the others must not carry one, so the
    -- window is present exactly when the rule is windowed.
    CONSTRAINT alert_rules_window_matches_type
        CHECK ((rule_type = 'change_pct') = (window_seconds IS NOT NULL))
);

-- The Alerting service loads rules by instrument and the API lists them per
-- instrument; index the foreign key, which also backs the referential-integrity
-- checks PostgreSQL does not index automatically.
CREATE INDEX idx_alert_rules_instrument ON alert_rules (instrument_id);

CREATE TABLE alert_history (
    id              BIGINT         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- History belongs to its rule: deleting a rule removes its fired-alert log.
    -- The audit trail is scoped to the rule's lifetime, which keeps the DELETE
    -- endpoint a simple, single-row operation for the single operator.
    rule_id         BIGINT         NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    fired_at        TIMESTAMPTZ    NOT NULL DEFAULT now(),
    observed_value  NUMERIC(20, 8) NOT NULL,
    delivery_status TEXT           NOT NULL
                        CHECK (delivery_status IN ('sent', 'failed', 'retrying'))
);

-- History is read back per rule, newest first, and the cascade delete above needs
-- the foreign key indexed to avoid a full scan per removed rule.
CREATE INDEX idx_alert_history_rule_fired ON alert_history (rule_id, fired_at DESC);

COMMENT ON TABLE alert_rules IS
    'User-configured alerting conditions evaluated against the live tick stream.';
COMMENT ON TABLE alert_history IS
    'Audit log of fired alerts and their notification delivery status.';

COMMIT;
