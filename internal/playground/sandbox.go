// Package playground executes arbitrary user SQL for the SQL Playground under a
// layered sandbox. Because the input is untrusted, safety comes from several
// independent controls rather than any single check:
//
//   - a dedicated, bounded connection pool, so untrusted SQL can neither exhaust
//     nor poison the connections that serve the rest of the API;
//   - a read-only transaction, so no statement can write;
//   - a least-privilege role (playground_readonly) assumed via SET LOCAL ROLE,
//     so only whitelisted tables are visible and no DDL or DML is permitted;
//   - a per-transaction statement timeout, so a runaway query is canceled;
//   - a connection reset (DISCARD ALL) after every query, so session-scoped side
//     effects (advisory locks, prepared statements, temp objects) cannot ride the
//     pooled connection into the next execution;
//   - a row and byte cap on the result, enforced by wrapping the query.
//
// A static pre-check rejects obviously non-read statements early for a clearer
// error, but the transaction and role are the real enforcement.
package playground

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sandbox limits and identifiers. These bound the work and the result a single
// query can produce.
const (
	sandboxRole         = "playground_readonly"
	defaultQueryTimeout = 5 * time.Second
	// contextMargin lets the database statement_timeout fire first (a clean
	// 57014 error) while the context deadline backstops any lower-level hang.
	contextMargin = 2 * time.Second
	// resetTimeout bounds the post-query connection reset independently of the
	// caller's (possibly already expired) context.
	resetTimeout   = 2 * time.Second
	maxRows        = 1000
	maxCellBytes   = 64 << 10 // 64 KiB, truncates a single oversized cell
	maxResultBytes = 8 << 20  // 8 MiB, hard cap on the raw wire bytes read into a result
)

// execMode runs every sandbox statement without pgx's named prepared-statement
// cache, which the post-query DISCARD ALL would otherwise invalidate (see the
// note in Execute).
const execMode = pgx.QueryExecModeExec

// Static validation errors, surfaced to the caller as client errors.
var (
	ErrEmptyQuery  = errors.New("playground: query is empty")
	ErrNotReadOnly = errors.New("playground: only SELECT, WITH, VALUES or TABLE queries are allowed")
)

// readOnlyPrefixes are the leading keywords of statements that can be wrapped as
// a subquery and are read-oriented. Data-modifying CTEs (WITH ... INSERT) are
// still blocked by the read-only transaction and the role's lack of privileges.
var readOnlyPrefixes = []string{"SELECT", "WITH", "VALUES", "TABLE"}

// QueryError is a database-reported problem with the user's SQL (syntax,
// permission, timeout, ...). Its message is safe to return to the caller: it
// describes the caller's own query, not the server's internals.
type QueryError struct {
	Code    string
	Message string
}

func (e *QueryError) Error() string { return e.Message }

// Column is a result column's name and PostgreSQL type.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Result is the outcome of a sandboxed query.
type Result struct {
	Columns   []Column `json:"columns"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"row_count"`
	Truncated bool     `json:"truncated"`
}

// Sandbox executes queries against a connection pool under the sandbox controls.
type Sandbox struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

// New constructs a Sandbox over pool. The pool should be dedicated to the
// Playground and bounded (a small MaxConns), so untrusted SQL cannot exhaust or
// poison the connections that serve the rest of the API; its role must be a
// member of playground_readonly so it can assume it via SET LOCAL ROLE.
func New(pool *pgxpool.Pool) *Sandbox {
	return &Sandbox{pool: pool, queryTimeout: defaultQueryTimeout}
}

// Execute validates, sandboxes and runs query, returning the capped result. A
// bad or non-read query yields a static error or a *QueryError; infrastructure
// failures yield a wrapped internal error.
func (s *Sandbox) Execute(ctx context.Context, query string) (*Result, error) {
	stmt, err := sanitize(query)
	if err != nil {
		return nil, err
	}

	// A context deadline backstops the database statement_timeout: it bounds the
	// whole operation even if a lower level hangs.
	ctx, cancel := context.WithTimeout(ctx, s.queryTimeout+contextMargin)
	defer cancel()

	// Acquire an explicit connection so the post-query reset runs on the same
	// backend before it returns to the pool.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("playground: acquire: %w", err)
	}
	defer conn.Release()
	// Session-scoped side effects (advisory locks, prepared statements, temp
	// objects) survive a transaction rollback and would otherwise ride the pooled
	// connection into the next query. DISCARD ALL clears them; the dedicated pool
	// already confines any residue to the Playground itself. Runs after the
	// rollback below (deferred later, so it runs first) and before Release.
	defer resetConn(conn)

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("playground: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// The statement timeout and role are transaction-local and vanish on
	// rollback. Both operands are trusted (a constant role name, an integer
	// millisecond count), never user input.
	//
	// Every statement runs under execMode: the post-query DISCARD ALL deallocates
	// prepared statements, so the default statement-caching mode would later reuse
	// a cached statement the server has already dropped (SQLSTATE 26000). Exec
	// mode skips the named-statement cache while keeping binary decoding, and the
	// sandbox's one-off SQL gains nothing from caching anyway.
	//
	// Safety of the layering, verified against PostgreSQL: the subquery wrap
	// rejects top-level data-modifying CTEs outright; SET LOCAL ROLE and the
	// timeout are themselves queries, so the read-only mode can no longer be
	// disabled; every table and function privilege is checked at plan time as
	// playground_readonly; and the statement timeout of the running query is
	// fixed when it starts. A query that escalates its role via set_config is
	// therefore inert: it can neither read a non-whitelisted object nor write.
	timeoutMS := s.queryTimeout.Milliseconds()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeoutMS), execMode); err != nil {
		return nil, fmt.Errorf("playground: set timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+sandboxRole, execMode); err != nil {
		return nil, fmt.Errorf("playground: set role: %w", err)
	}

	// Wrapping in a bounded subquery caps the row count regardless of the user's
	// own LIMIT and turns any multi-statement input into a syntax error.
	wrapped := fmt.Sprintf("SELECT * FROM (%s) AS _sandbox LIMIT %d", stmt, maxRows+1)
	rows, err := tx.Query(ctx, wrapped, execMode)
	if err != nil {
		return nil, asQueryError(err)
	}
	defer rows.Close()

	result, err := scanResult(rows)
	if err != nil {
		return nil, asQueryError(err)
	}
	return result, nil
}

// Validate reports whether query is an acceptable Playground statement:
// non-empty and read-only. It applies the same rule the sandbox enforces before
// execution, so a query rejected here would never run. It returns ErrEmptyQuery
// or ErrNotReadOnly on failure, and nil when the query is acceptable.
func Validate(query string) error {
	_, err := sanitize(query)
	return err
}

// sanitize trims the statement, strips a trailing semicolon and requires a
// read-only leading keyword.
func sanitize(query string) (string, error) {
	stmt := strings.TrimSpace(query)
	if stmt == "" {
		return "", ErrEmptyQuery
	}
	// Strip a trailing semicolon (and any surrounding whitespace) so the wrap
	// stays valid; a semicolon inside a literal is left untouched.
	stmt = strings.TrimRight(stmt, "; \t\r\n")
	if stmt == "" {
		return "", ErrEmptyQuery
	}
	if !hasReadOnlyPrefix(stmt) {
		return "", ErrNotReadOnly
	}
	return stmt, nil
}

// hasReadOnlyPrefix reports whether the statement's first keyword is one of the
// allowed read-oriented keywords, skipping any leading parentheses (a
// parenthesized or set-operation query) and whitespace.
func hasReadOnlyPrefix(stmt string) bool {
	stmt = strings.TrimLeft(stmt, "( \t\r\n")
	first := stmt
	if i := strings.IndexFunc(stmt, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '('
	}); i >= 0 {
		first = stmt[:i]
	}
	first = strings.ToUpper(first)
	for _, p := range readOnlyPrefixes {
		if first == p {
			return true
		}
	}
	return false
}

// scanResult reads rows into a Result, enforcing the row and byte caps.
func scanResult(rows pgx.Rows) (*Result, error) {
	typeMap := rows.Conn().TypeMap()
	fields := rows.FieldDescriptions()
	columns := make([]Column, len(fields))
	for i, fd := range fields {
		name := fmt.Sprintf("oid:%d", fd.DataTypeOID)
		if t, ok := typeMap.TypeForOID(fd.DataTypeOID); ok {
			name = t.Name
		}
		columns[i] = Column{Name: fd.Name, Type: name}
	}

	out := make([][]any, 0)
	truncated := false
	total := 0
	for rows.Next() {
		if len(out) >= maxRows {
			truncated = true
			break
		}
		// Budget on the raw wire size of the row, measured before pgx decodes it.
		// RawValues counts every column uniformly (not just text), so the cap is a
		// true bound on the result rather than on its string cells alone. Checking
		// it before the decode also stops a single pathological cell (e.g.
		// SELECT repeat('x', 1e9)) from being materialized a second time as a Go
		// value: the row is one buffered protocol message, itself bounded by
		// PostgreSQL's 1 GiB per-field limit, and we skip it rather than doubling it.
		rowBytes := 0
		for _, rv := range rows.RawValues() {
			rowBytes += len(rv)
		}
		if total+rowBytes > maxResultBytes {
			truncated = true
			break
		}
		total += rowBytes

		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make([]any, len(values))
		for i, v := range values {
			row[i] = normalizeValue(v)
		}
		out = append(out, row)
	}
	if !truncated {
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	return &Result{Columns: columns, Rows: out, RowCount: len(out), Truncated: truncated}, nil
}

// normalizeValue converts a pgx-decoded value into a JSON-friendly form:
// timestamps and numerics become strings (preserving precision), binary becomes
// text or base64, and primitives pass through. Oversized strings are truncated.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case []byte:
		if utf8.Valid(x) {
			return capString(string(x))
		}
		return base64.StdEncoding.EncodeToString(x)
	case string:
		return capString(x)
	case pgtype.Numeric:
		if !x.Valid {
			return nil
		}
		val, err := x.Value()
		if err != nil {
			return nil
		}
		return val
	case int64:
		// 64-bit integers can exceed JavaScript's safe integer range (2^53), so
		// render them as strings to preserve precision on the wire, as numerics
		// and timestamps already are.
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case bool, int16, int32, uint16, uint32, float32, float64:
		// Smaller scalars fit a JSON number exactly and pass through unchanged.
		return v
	default:
		// Exotic decoded types (arrays, uuid, json, composites, ...) may not be
		// JSON-encodable and could otherwise crash the response encoder; render
		// them as a capped string so the endpoint always returns a valid result.
		return capString(fmt.Sprintf("%v", v))
	}
}

// capString truncates a cell that exceeds the per-cell byte cap, cutting on a
// UTF-8 rune boundary so the result is never a half-encoded rune.
func capString(s string) string {
	if len(s) <= maxCellBytes {
		return s
	}
	cut := maxCellBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...(truncated)"
}

// resetConn clears any session-scoped state left on the connection before it
// returns to the pool. It runs on a fresh context because the caller's may have
// already expired. A failure here means the connection is unhealthy; the pool
// discards such connections on release, so the error is not actionable.
func resetConn(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()
	_, _ = conn.Exec(ctx, "DISCARD ALL", execMode)
}

// asQueryError converts a PostgreSQL error into a *QueryError whose message is
// safe to show the caller; non-database errors are returned unchanged.
func asQueryError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return &QueryError{Code: pgErr.Code, Message: pgErr.Message}
	}
	return err
}
