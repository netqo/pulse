package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/db"
)

// fakeStore implements QueryStore with a hook per method.
type fakeStore struct {
	saveFn func(ctx context.Context, in db.SaveQueryInput) (db.SavedQuery, error)
	loadFn func(ctx context.Context, id string) (db.SavedQuery, error)
}

func (f *fakeStore) SaveQuery(ctx context.Context, in db.SaveQueryInput) (db.SavedQuery, error) {
	return f.saveFn(ctx, in)
}

func (f *fakeStore) SavedQuery(ctx context.Context, id string) (db.SavedQuery, error) {
	return f.loadFn(ctx, id)
}

func newStoreServer(t *testing.T, store QueryStore) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Config{Reader: &fakeReader{}, Queries: store, Logger: logger, Registerer: prometheus.NewRegistry()}).Handler()
}

func postSave(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSaveQueryOK(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	var captured db.SaveQueryInput
	h := newStoreServer(t, &fakeStore{
		saveFn: func(_ context.Context, in db.SaveQueryInput) (db.SavedQuery, error) {
			captured = in
			return db.SavedQuery{ID: id, SQL: in.SQL}, nil
		},
	})
	rec := postSave(t, h, `{"query":"SELECT 1","title":"one","chart_config":{"type":"line"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	var res saveQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.ID != id || res.URL != "/api/v1/playground/q/"+id {
		t.Errorf("unexpected response: %+v", res)
	}
	if captured.SQL != "SELECT 1" || captured.Title == nil || *captured.Title != "one" {
		t.Errorf("store input = %+v", captured)
	}
	if string(captured.ChartConfig) != `{"type":"line"}` {
		t.Errorf("chart_config passed through = %s", captured.ChartConfig)
	}
}

func TestSaveQueryRejectsInvalidQuery(t *testing.T) {
	cases := map[string]string{
		"empty":    `{"query":"   "}`,
		"not-read": `{"query":"INSERT INTO instruments VALUES (1)"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newStoreServer(t, &fakeStore{
				saveFn: func(context.Context, db.SaveQueryInput) (db.SavedQuery, error) {
					t.Error("store should not be called on an invalid query")
					return db.SavedQuery{}, nil
				},
			})
			rec := postSave(t, h, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestSaveQueryRejectsOversizedFields(t *testing.T) {
	cases := map[string]string{
		"title": `{"query":"SELECT 1","title":"` + strings.Repeat("x", maxTitleChars+1) + `"}`,
		"query": `{"query":"SELECT '` + strings.Repeat("x", maxSQLChars) + `'"}`,
		"chart": `{"query":"SELECT 1","chart_config":{"blob":"` + strings.Repeat("x", maxChartBytes) + `"}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			guard := &fakeStore{saveFn: func(context.Context, db.SaveQueryInput) (db.SavedQuery, error) {
				t.Error("store should not be called on an oversized field")
				return db.SavedQuery{}, nil
			}}
			rec := postSave(t, newStoreServer(t, guard), body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestSaveQueryBadBody(t *testing.T) {
	h := newStoreServer(t, &fakeStore{
		saveFn: func(context.Context, db.SaveQueryInput) (db.SavedQuery, error) {
			t.Error("store should not be called on a bad body")
			return db.SavedQuery{}, nil
		},
	})
	if rec := postSave(t, h, `not json`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSaveQueryStoreError(t *testing.T) {
	h := newStoreServer(t, &fakeStore{
		saveFn: func(context.Context, db.SaveQueryInput) (db.SavedQuery, error) {
			return db.SavedQuery{}, context.DeadlineExceeded
		},
	})
	if rec := postSave(t, h, `{"query":"SELECT 1"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestLoadQueryOK(t *testing.T) {
	const id = "22222222-2222-2222-2222-222222222222"
	title := "saved"
	h := newStoreServer(t, &fakeStore{
		loadFn: func(_ context.Context, gotID string) (db.SavedQuery, error) {
			if gotID != id {
				t.Errorf("id = %q, want %q", gotID, id)
			}
			return db.SavedQuery{ID: id, Title: &title, SQL: "SELECT 1", ChartConfig: []byte(`{"type":"bar"}`)}, nil
		},
	})
	rec := do(t, h, "/api/v1/playground/q/"+id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var dto savedQueryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.ID != id || dto.Query != "SELECT 1" || dto.Title == nil || *dto.Title != "saved" {
		t.Errorf("unexpected dto: %+v", dto)
	}
	if string(dto.ChartConfig) != `{"type":"bar"}` {
		t.Errorf("chart_config = %s", dto.ChartConfig)
	}
}

func TestLoadQueryErrors(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"invalid": {db.ErrInvalidID, http.StatusBadRequest},
		"missing": {db.ErrNotFound, http.StatusNotFound},
		"failure": {context.DeadlineExceeded, http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newStoreServer(t, &fakeStore{
				loadFn: func(context.Context, string) (db.SavedQuery, error) {
					return db.SavedQuery{}, tc.err
				},
			})
			rec := do(t, h, "/api/v1/playground/q/whatever")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
