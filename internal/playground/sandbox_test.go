package playground

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"select", "SELECT 1", "SELECT 1", nil},
		{"lowercase with", "with t as (select 1) select * from t", "with t as (select 1) select * from t", nil},
		{"trailing semicolon", "SELECT 1;", "SELECT 1", nil},
		{"trailing semicolon and spaces", "SELECT 1;  \n", "SELECT 1", nil},
		{"embedded semicolon literal", "SELECT ';'", "SELECT ';'", nil},
		{"leading paren", "(SELECT 1)", "(SELECT 1)", nil},
		{"empty", "   ", "", ErrEmptyQuery},
		{"only semicolon", ";", "", ErrEmptyQuery},
		{"insert", "INSERT INTO instruments VALUES (1)", "", ErrNotReadOnly},
		{"update", "UPDATE prices SET price = 0", "", ErrNotReadOnly},
		{"drop", "DROP TABLE prices", "", ErrNotReadOnly},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitize(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("sanitize(%q) err = %v, want %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSandboxIntegration exercises the real sandbox against PostgreSQL. Skipped
// unless DATABASE_URL is set (CI's Go job provides a superuser-owned database
// whose role can assume playground_readonly).
func TestSandboxIntegration(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping sandbox integration test")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	setupSandboxFixture(ctx, t, pool)

	sb := New(pool)
	sb.queryTimeout = 300 * time.Millisecond // keep the timeout test fast

	t.Run("select from a granted table works", func(t *testing.T) {
		res, err := sb.Execute(ctx, "SELECT id, label, amount FROM playground_probe ORDER BY id")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.RowCount != 1 || res.Rows[0][1] != "a" || res.Rows[0][2] != "1.50000000" {
			t.Errorf("unexpected result: %+v", res.Rows)
		}
	})

	t.Run("select returns typed columns and rows", func(t *testing.T) {
		// The final column is 2^53 + 1, past JavaScript's safe integer range, to
		// pin that bigints are rendered as exact strings rather than JSON numbers.
		res, err := sb.Execute(ctx,
			"SELECT 42::bigint AS n, 'hi'::text AS s, 1.5::numeric AS d, NULL::text AS e, 9007199254740993::bigint AS big")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if len(res.Columns) != 5 || res.Columns[0].Name != "n" {
			t.Fatalf("columns = %+v", res.Columns)
		}
		if res.RowCount != 1 {
			t.Fatalf("row count = %d, want 1", res.RowCount)
		}
		row := res.Rows[0]
		if row[0] != "42" { // bigint rendered as a string to preserve precision
			t.Errorf("bigint cell = %v (%T), want \"42\"", row[0], row[0])
		}
		if row[1] != "hi" {
			t.Errorf("text cell = %v, want hi", row[1])
		}
		if row[2] != "1.5" { // numeric rendered as an exact decimal string
			t.Errorf("numeric cell = %v (%T), want \"1.5\"", row[2], row[2])
		}
		if row[3] != nil {
			t.Errorf("null cell = %v, want nil", row[3])
		}
		if row[4] != "9007199254740993" {
			t.Errorf("large bigint cell = %v, want \"9007199254740993\" (precision preserved)", row[4])
		}
	})

	t.Run("writes are rejected statically", func(t *testing.T) {
		_, err := sb.Execute(ctx, "INSERT INTO playground_probe VALUES (2, 'b', 2.0)")
		if !errors.Is(err, ErrNotReadOnly) {
			t.Fatalf("insert err = %v, want ErrNotReadOnly", err)
		}
	})

	t.Run("data-modifying CTE is blocked by the sandbox", func(t *testing.T) {
		// Passes the static prefix check (WITH) but must fail at the DB layer:
		// the read-only transaction and the role's lack of INSERT both stop it.
		_, err := sb.Execute(ctx,
			"WITH x AS (INSERT INTO playground_probe VALUES (3, 'c', 3.0) RETURNING id) SELECT * FROM x")
		var qErr *QueryError
		if !errors.As(err, &qErr) {
			t.Fatalf("CTE-write err = %v, want *QueryError from the sandbox", err)
		}
	})

	t.Run("non-whitelisted table is denied", func(t *testing.T) {
		_, err := sb.Execute(ctx, "SELECT count(*) FROM pg_authid")
		var qErr *QueryError
		if !errors.As(err, &qErr) || qErr.Code != "42501" {
			t.Fatalf("forbidden-table err = %v, want permission denied (42501)", err)
		}
	})

	t.Run("statement timeout cancels a slow query", func(t *testing.T) {
		_, err := sb.Execute(ctx, "SELECT pg_sleep(2)")
		var qErr *QueryError
		if !errors.As(err, &qErr) || qErr.Code != "57014" {
			t.Fatalf("slow-query err = %v, want statement timeout (57014)", err)
		}
	})

	t.Run("row cap truncates and reports it", func(t *testing.T) {
		res, err := sb.Execute(ctx, "SELECT generate_series(1, 5000) AS n")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.RowCount != maxRows || !res.Truncated {
			t.Errorf("row cap = %d truncated=%v, want %d truncated=true", res.RowCount, res.Truncated, maxRows)
		}
	})

	t.Run("byte cap truncates a large multi-row result", func(t *testing.T) {
		// Each row carries ~100 KiB, so the cumulative raw size crosses the 8 MiB
		// budget well before the 1000-row cap: the result stops partway and says so.
		res, err := sb.Execute(ctx, "SELECT repeat('x', 100000) FROM generate_series(1, 200)")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Truncated || res.RowCount == 0 || res.RowCount >= maxRows {
			t.Errorf("byte cap = %d rows truncated=%v, want a partial truncated result", res.RowCount, res.Truncated)
		}
	})

	t.Run("a single oversized cell is dropped, not doubled in memory", func(t *testing.T) {
		// One cell larger than the whole-result budget is skipped before it is
		// decoded into a second copy; the caller still gets a well-formed truncated
		// result rather than an error or a huge allocation.
		res, err := sb.Execute(ctx, "SELECT repeat('x', 10000000) AS big")
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !res.Truncated || res.RowCount != 0 {
			t.Errorf("oversized cell = %d rows truncated=%v, want 0 rows truncated=true", res.RowCount, res.Truncated)
		}
	})

	t.Run("multi-statement input is rejected", func(t *testing.T) {
		_, err := sb.Execute(ctx, "SELECT 1; DROP TABLE playground_probe")
		var qErr *QueryError
		if !errors.As(err, &qErr) {
			t.Fatalf("multi-statement err = %v, want *QueryError (syntax)", err)
		}
	})

	t.Run("session-scoped state does not leak across queries", func(t *testing.T) {
		// A single-connection pool forces the second query onto the same backend
		// as the first, so a session-level advisory lock left behind would still
		// be held unless the post-query reset released it.
		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			t.Fatalf("parse config: %v", err)
		}
		cfg.MaxConns = 1
		single, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("pool: %v", err)
		}
		defer single.Close()
		sb := New(single)

		if _, err := sb.Execute(ctx, "SELECT pg_advisory_lock(4242)"); err != nil {
			t.Fatalf("acquire advisory lock: %v", err)
		}
		res, err := sb.Execute(ctx, "SELECT count(*) AS n FROM pg_locks WHERE locktype = 'advisory'")
		if err != nil {
			t.Fatalf("count advisory locks: %v", err)
		}
		// count(*) is a bigint, rendered as a string to preserve precision.
		if got := res.Rows[0][0]; got != "0" {
			t.Errorf("advisory locks held after reset = %v, want \"0\"", got)
		}
	})
}

// TestSandboxReusesConnectionAfterReset guards the exec-mode fix: with a single
// pooled connection, the same query run repeatedly reuses the backend, and the
// post-query DISCARD ALL would invalidate a cached prepared statement (SQLSTATE
// 26000) unless the sandbox runs its statements without the statement cache.
func TestSandboxReusesConnectionAfterReset(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping database integration test")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()
	setupSandboxFixture(ctx, t, pool)
	sb := New(pool)

	for i := 0; i < 3; i++ {
		if _, err := sb.Execute(ctx, "SELECT id, label FROM playground_probe"); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
}

// setupSandboxFixture provisions a self-contained fixture: the
// playground_readonly role (as migration 0003 does) plus a dedicated probe
// table it may read. It deliberately avoids the shared instruments/prices tables
// so it does not race the db package's integration tests, which run against the
// same database in parallel.
func setupSandboxFixture(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	stmts := []string{
		`DO $$ BEGIN
		    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'playground_readonly') THEN
		        CREATE ROLE playground_readonly NOLOGIN;
		    END IF;
		END $$;`,
		`GRANT playground_readonly TO CURRENT_USER`,
		`DROP TABLE IF EXISTS playground_probe`,
		`CREATE TABLE playground_probe (id BIGINT, label TEXT, amount NUMERIC(20, 8))`,
		`INSERT INTO playground_probe VALUES (1, 'a', 1.5)`,
		`GRANT SELECT ON playground_probe TO playground_readonly`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("fixture setup failed on %q: %v", s, err)
		}
	}
}
