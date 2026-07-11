package db

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAlertsIntegration exercises the alert-rule and alert-history data access
// against real PostgreSQL. Skipped unless DATABASE_URL is set.
func TestAlertsIntegration(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database integration test")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	applyAlertsSchema(ctx, t, pool)

	d, err := New(ctx, url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	instrumentID, err := d.UpsertInstrument(ctx, "ALERTTESTUSDT")
	if err != nil {
		t.Fatalf("UpsertInstrument: %v", err)
	}

	t.Run("create, list and enabled round-trip", func(t *testing.T) {
		rule, err := d.CreateAlertRule(ctx, CreateAlertRuleInput{
			InstrumentID: instrumentID,
			RuleType:     RuleTypePriceBelow,
			Threshold:    "25000.50000000",
			Channel:      ChannelTelegram,
			Target:       "123456",
		})
		if err != nil {
			t.Fatalf("CreateAlertRule: %v", err)
		}
		if rule.ID == 0 || rule.CreatedAt.IsZero() {
			t.Fatalf("stored rule missing id/timestamp: %+v", rule)
		}
		if rule.Threshold != "25000.50000000" || rule.WindowSeconds != nil || !rule.IsEnabled {
			t.Errorf("stored rule = %+v", rule)
		}

		if got := findRule(d.mustListRules(ctx, t), rule.ID); got == nil {
			t.Fatalf("rule %d missing from ListAlertRules", rule.ID)
		}

		enabled, err := d.EnabledAlertRules(ctx)
		if err != nil {
			t.Fatalf("EnabledAlertRules: %v", err)
		}
		var found *EnabledRule
		for i := range enabled {
			if enabled[i].ID == rule.ID {
				found = &enabled[i]
			}
		}
		if found == nil {
			t.Fatalf("rule %d missing from EnabledAlertRules", rule.ID)
		}
		if found.Symbol != "ALERTTESTUSDT" {
			t.Errorf("enabled rule symbol = %q, want ALERTTESTUSDT", found.Symbol)
		}
	})

	t.Run("change_pct rule carries its window", func(t *testing.T) {
		window := int32(300)
		rule, err := d.CreateAlertRule(ctx, CreateAlertRuleInput{
			InstrumentID:  instrumentID,
			RuleType:      RuleTypeChangePct,
			Threshold:     "5.00000000",
			WindowSeconds: &window,
			Channel:       ChannelWebhook,
			Target:        "https://example.com/hook",
		})
		if err != nil {
			t.Fatalf("CreateAlertRule: %v", err)
		}
		if rule.WindowSeconds == nil || *rule.WindowSeconds != 300 {
			t.Errorf("window_seconds = %v, want 300", rule.WindowSeconds)
		}
	})

	t.Run("record history then delete cascades and reports missing", func(t *testing.T) {
		rule, err := d.CreateAlertRule(ctx, CreateAlertRuleInput{
			InstrumentID: instrumentID,
			RuleType:     RuleTypePriceAbove,
			Threshold:    "30000.00000000",
			Channel:      ChannelTelegram,
			Target:       "123456",
		})
		if err != nil {
			t.Fatalf("CreateAlertRule: %v", err)
		}

		hist, err := d.RecordAlert(ctx, AlertHistoryInput{
			RuleID:         rule.ID,
			ObservedValue:  "30001.00000000",
			DeliveryStatus: DeliverySent,
		})
		if err != nil {
			t.Fatalf("RecordAlert: %v", err)
		}
		if hist.ID == 0 || hist.FiredAt.IsZero() || hist.ObservedValue != "30001.00000000" {
			t.Errorf("stored history = %+v", hist)
		}

		// Deleting the rule cascades to its history (no FK violation).
		if err := d.DeleteAlertRule(ctx, rule.ID); err != nil {
			t.Fatalf("DeleteAlertRule: %v", err)
		}
		if err := d.DeleteAlertRule(ctx, rule.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("second delete err = %v, want ErrNotFound", err)
		}
		if err := d.DeleteAlertRule(ctx, 9_999_999); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete of unknown id err = %v, want ErrNotFound", err)
		}
	})

	t.Run("window-matches-type is enforced by the schema", func(t *testing.T) {
		_, err := d.CreateAlertRule(ctx, CreateAlertRuleInput{
			InstrumentID: instrumentID,
			RuleType:     RuleTypeChangePct, // windowed type without a window
			Threshold:    "5",
			Channel:      ChannelTelegram,
			Target:       "1",
		})
		if err == nil {
			t.Error("change_pct without window_seconds was accepted, want a constraint error")
		}

		window := int32(60)
		_, err = d.CreateAlertRule(ctx, CreateAlertRuleInput{
			InstrumentID:  instrumentID,
			RuleType:      RuleTypePriceBelow, // non-windowed type with a window
			Threshold:     "1",
			WindowSeconds: &window,
			Channel:       ChannelTelegram,
			Target:        "1",
		})
		if err == nil {
			t.Error("price_below with window_seconds was accepted, want a constraint error")
		}
	})
}

func (d *DB) mustListRules(ctx context.Context, t *testing.T) []AlertRule {
	t.Helper()
	rules, err := d.ListAlertRules(ctx)
	if err != nil {
		t.Fatalf("ListAlertRules: %v", err)
	}
	return rules
}

func findRule(rules []AlertRule, id int64) *AlertRule {
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}

// applyAlertsSchema resets and applies the alerting tables so the test runs
// against a known state. alert_history is dropped first because it references
// alert_rules.
func applyAlertsSchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS alert_history; DROP TABLE IF EXISTS alert_rules`); err != nil {
		t.Fatalf("drop alert tables: %v", err)
	}
	up, err := os.ReadFile("../../migrations/0005_alerting.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}
