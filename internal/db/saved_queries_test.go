package db

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSavedQueriesIntegration exercises the save/load round-trip against real
// PostgreSQL. Skipped unless DATABASE_URL is set.
func TestSavedQueriesIntegration(t *testing.T) {
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
	applySavedQueriesSchema(ctx, t, pool)

	d, err := New(ctx, url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	t.Run("save and load a full query round-trips", func(t *testing.T) {
		title := "gainers"
		saved, err := d.SaveQuery(ctx, SaveQueryInput{
			Title:       &title,
			SQL:         "SELECT symbol FROM instruments",
			ChartConfig: []byte(`{"type":"line"}`),
		})
		if err != nil {
			t.Fatalf("SaveQuery: %v", err)
		}
		if saved.ID == "" || saved.CreatedAt.IsZero() {
			t.Fatalf("saved record missing id/timestamp: %+v", saved)
		}

		got, err := d.SavedQuery(ctx, saved.ID)
		if err != nil {
			t.Fatalf("SavedQuery: %v", err)
		}
		if got.SQL != "SELECT symbol FROM instruments" || got.Title == nil || *got.Title != title {
			t.Errorf("loaded record = %+v", got)
		}
		var cfg map[string]string
		if err := json.Unmarshal(got.ChartConfig, &cfg); err != nil {
			t.Fatalf("chart_config not valid JSON: %v", err)
		}
		if cfg["type"] != "line" {
			t.Errorf("chart_config = %v, want type=line", cfg)
		}
	})

	t.Run("optional fields persist as NULL", func(t *testing.T) {
		saved, err := d.SaveQuery(ctx, SaveQueryInput{SQL: "SELECT 1"})
		if err != nil {
			t.Fatalf("SaveQuery: %v", err)
		}
		got, err := d.SavedQuery(ctx, saved.ID)
		if err != nil {
			t.Fatalf("SavedQuery: %v", err)
		}
		if got.Title != nil {
			t.Errorf("title = %v, want nil", *got.Title)
		}
		if got.ChartConfig != nil {
			t.Errorf("chart_config = %s, want nil", got.ChartConfig)
		}
	})

	t.Run("unknown id is not found", func(t *testing.T) {
		_, err := d.SavedQuery(ctx, "00000000-0000-0000-0000-000000000000")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("malformed id is rejected", func(t *testing.T) {
		_, err := d.SavedQuery(ctx, "not-a-uuid")
		if !errors.Is(err, ErrInvalidID) {
			t.Errorf("err = %v, want ErrInvalidID", err)
		}
	})
}

// applySavedQueriesSchema resets and applies the saved_queries table so the test
// runs against a known state, independent of the price/instrument schema.
func applySavedQueriesSchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS saved_queries`); err != nil {
		t.Fatalf("drop saved_queries: %v", err)
	}
	up, err := os.ReadFile("../../migrations/0004_saved_queries.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}
