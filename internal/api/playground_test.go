package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/playground"
)

// fakeExecutor implements PlaygroundExecutor with a hook per test.
type fakeExecutor struct {
	fn func(ctx context.Context, query string) (*playground.Result, error)
}

func (f *fakeExecutor) Execute(ctx context.Context, query string) (*playground.Result, error) {
	return f.fn(ctx, query)
}

func newPlaygroundServer(t *testing.T, exec PlaygroundExecutor) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reader := &fakeReader{}
	return New(reader, exec, nil, logger, prometheus.NewRegistry()).Handler()
}

func postQuery(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/query", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPlaygroundQueryOK(t *testing.T) {
	h := newPlaygroundServer(t, &fakeExecutor{
		fn: func(_ context.Context, query string) (*playground.Result, error) {
			if query != "SELECT 1" {
				t.Errorf("query = %q", query)
			}
			return &playground.Result{
				Columns:  []playground.Column{{Name: "n", Type: "int4"}},
				Rows:     [][]any{{float64(1)}},
				RowCount: 1,
			}, nil
		},
	})
	rec := postQuery(t, h, `{"query":"SELECT 1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var res playground.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.RowCount != 1 || len(res.Columns) != 1 || res.Columns[0].Name != "n" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestPlaygroundQueryValidationErrors(t *testing.T) {
	cases := map[string]error{
		"empty":    playground.ErrEmptyQuery,
		"not-read": playground.ErrNotReadOnly,
	}
	for name, sentinel := range cases {
		t.Run(name, func(t *testing.T) {
			h := newPlaygroundServer(t, &fakeExecutor{
				fn: func(context.Context, string) (*playground.Result, error) { return nil, sentinel },
			})
			rec := postQuery(t, h, `{"query":"x"}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestPlaygroundQueryReportsQueryError(t *testing.T) {
	h := newPlaygroundServer(t, &fakeExecutor{
		fn: func(context.Context, string) (*playground.Result, error) {
			return nil, &playground.QueryError{Code: "42601", Message: "syntax error at end of input"}
		},
	})
	rec := postQuery(t, h, `{"query":"SELECT"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body queryErrorDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "42601" || body.Error != "syntax error at end of input" {
		t.Errorf("unexpected error body: %+v", body)
	}
}

func TestPlaygroundQueryInternalError(t *testing.T) {
	h := newPlaygroundServer(t, &fakeExecutor{
		fn: func(context.Context, string) (*playground.Result, error) {
			return nil, errors.New("pool exhausted")
		},
	})
	rec := postQuery(t, h, `{"query":"SELECT 1"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestPlaygroundQueryBadBody(t *testing.T) {
	h := newPlaygroundServer(t, &fakeExecutor{
		fn: func(context.Context, string) (*playground.Result, error) {
			t.Error("executor should not run on a bad body")
			return nil, nil
		},
	})
	rec := postQuery(t, h, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
