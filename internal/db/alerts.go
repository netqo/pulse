package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/netqo/pulse/internal/db/sqlc"
)

// Alert rule types. Each names the condition under which a rule fires against the
// live price; the values mirror the alert_rules.rule_type CHECK constraint.
const (
	RuleTypePriceBelow = "price_below"
	RuleTypePriceAbove = "price_above"
	RuleTypeChangePct  = "change_pct"
	RuleTypeCrosses    = "crosses"
)

// Notification channels a fired alert can be delivered through; the values mirror
// the alert_rules.channel CHECK constraint.
const (
	ChannelTelegram = "telegram"
	ChannelDiscord  = "discord"
	ChannelWebhook  = "webhook"
)

// Delivery outcomes recorded for a fired alert; the values mirror the
// alert_history.delivery_status CHECK constraint.
const (
	DeliverySent     = "sent"
	DeliveryFailed   = "failed"
	DeliveryRetrying = "retrying"
)

// AlertRule is a user-configured alerting condition. Threshold is an exact
// decimal string (like prices) to preserve precision on the wire; WindowSeconds
// is non-nil only for windowed (change_pct) rules.
type AlertRule struct {
	ID            int64
	InstrumentID  int64
	RuleType      string
	Threshold     string
	WindowSeconds *int32
	Channel       string
	Target        string
	IsEnabled     bool
	CreatedAt     time.Time
}

// EnabledRule is an enabled AlertRule paired with its instrument symbol, which
// the Alerting engine uses to match rules to incoming ticks without a second
// lookup.
type EnabledRule struct {
	AlertRule
	Symbol string
}

// AlertHistory is one fired-alert audit record. ObservedValue is the exact
// decimal price that triggered the rule.
type AlertHistory struct {
	ID             int64
	RuleID         int64
	FiredAt        time.Time
	ObservedValue  string
	DeliveryStatus string
}

// CreateAlertRuleInput carries the fields required to persist a new rule. Its
// values are expected to be validated by the caller; the schema's CHECK
// constraints are the backstop.
type CreateAlertRuleInput struct {
	InstrumentID  int64
	RuleType      string
	Threshold     string
	WindowSeconds *int32
	Channel       string
	Target        string
}

// AlertHistoryInput carries the fields required to record a fired alert.
type AlertHistoryInput struct {
	RuleID         int64
	ObservedValue  string
	DeliveryStatus string
}

// CreateAlertRule persists a new alerting rule and returns the stored record.
func (d *DB) CreateAlertRule(ctx context.Context, in CreateAlertRuleInput) (AlertRule, error) {
	threshold, err := decimalNumeric(in.Threshold)
	if err != nil {
		return AlertRule{}, err
	}
	row, err := d.queries.CreateAlertRule(ctx, sqlc.CreateAlertRuleParams{
		InstrumentID:  in.InstrumentID,
		RuleType:      in.RuleType,
		Threshold:     threshold,
		WindowSeconds: optInt4(in.WindowSeconds),
		Channel:       in.Channel,
		Target:        in.Target,
	})
	if err != nil {
		return AlertRule{}, fmt.Errorf("db: create alert rule: %w", err)
	}
	return toAlertRule(row)
}

// ListAlertRules returns every alerting rule, newest first, for the management
// API.
func (d *DB) ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := d.queries.ListAlertRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: list alert rules: %w", err)
	}
	out := make([]AlertRule, 0, len(rows))
	for _, r := range rows {
		rule, err := toAlertRule(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

// EnabledAlertRules returns the enabled rules the Alerting service evaluates,
// each paired with its instrument symbol.
func (d *DB) EnabledAlertRules(ctx context.Context) ([]EnabledRule, error) {
	rows, err := d.queries.ListEnabledAlertRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: list enabled alert rules: %w", err)
	}
	out := make([]EnabledRule, 0, len(rows))
	for _, r := range rows {
		threshold, ok := numericToString(r.Threshold)
		if !ok {
			return nil, fmt.Errorf("db: alert rule %d has an invalid threshold", r.ID)
		}
		out = append(out, EnabledRule{
			AlertRule: AlertRule{
				ID:            r.ID,
				InstrumentID:  r.InstrumentID,
				RuleType:      r.RuleType,
				Threshold:     threshold,
				WindowSeconds: int4ToPtr(r.WindowSeconds),
				Channel:       r.Channel,
				Target:        r.Target,
				IsEnabled:     r.IsEnabled,
				CreatedAt:     r.CreatedAt.Time,
			},
			Symbol: r.Symbol,
		})
	}
	return out, nil
}

// DeleteAlertRule removes a rule by id, returning ErrNotFound when no rule has
// that id.
func (d *DB) DeleteAlertRule(ctx context.Context, id int64) error {
	if _, err := d.queries.DeleteAlertRule(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("db: delete alert rule %d: %w", id, err)
	}
	return nil
}

// RecordAlert persists a fired-alert audit record and returns it.
func (d *DB) RecordAlert(ctx context.Context, in AlertHistoryInput) (AlertHistory, error) {
	observed, err := decimalNumeric(in.ObservedValue)
	if err != nil {
		return AlertHistory{}, err
	}
	row, err := d.queries.InsertAlertHistory(ctx, sqlc.InsertAlertHistoryParams{
		RuleID:         in.RuleID,
		ObservedValue:  observed,
		DeliveryStatus: in.DeliveryStatus,
	})
	if err != nil {
		return AlertHistory{}, fmt.Errorf("db: record alert: %w", err)
	}
	observedStr, ok := numericToString(row.ObservedValue)
	if !ok {
		return AlertHistory{}, fmt.Errorf("db: alert history %d has an invalid observed value", row.ID)
	}
	return AlertHistory{
		ID:             row.ID,
		RuleID:         row.RuleID,
		FiredAt:        row.FiredAt.Time,
		ObservedValue:  observedStr,
		DeliveryStatus: row.DeliveryStatus,
	}, nil
}

// toAlertRule converts a stored rule row into the domain type, rendering its
// NUMERIC threshold as an exact decimal string.
func toAlertRule(r sqlc.AlertRule) (AlertRule, error) {
	threshold, ok := numericToString(r.Threshold)
	if !ok {
		return AlertRule{}, fmt.Errorf("db: alert rule %d has an invalid threshold", r.ID)
	}
	return AlertRule{
		ID:            r.ID,
		InstrumentID:  r.InstrumentID,
		RuleType:      r.RuleType,
		Threshold:     threshold,
		WindowSeconds: int4ToPtr(r.WindowSeconds),
		Channel:       r.Channel,
		Target:        r.Target,
		IsEnabled:     r.IsEnabled,
		CreatedAt:     r.CreatedAt.Time,
	}, nil
}

// optInt4 wraps an optional int32 as a pgtype.Int4, yielding a NULL when the
// pointer is nil.
func optInt4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

// int4ToPtr renders a nullable integer column as an optional int32, yielding nil
// for a NULL value.
func int4ToPtr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	n := v.Int32
	return &n
}
