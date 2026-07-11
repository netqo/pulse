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

const testToken = "s3cr3t-operator-token"

// fakeAlertStore implements AlertStore with configurable results and captures the
// arguments the handlers pass, so each test can assert on them.
type fakeAlertStore struct {
	instrumentID  int64
	instrumentErr error
	createResult  db.AlertRule
	created       *db.CreateAlertRuleInput
	rules         []db.RuleWithSymbol
	deleteErr     error
	deletedID     int64
}

func (f *fakeAlertStore) InstrumentIDBySymbol(context.Context, string) (int64, error) {
	return f.instrumentID, f.instrumentErr
}

func (f *fakeAlertStore) CreateAlertRule(_ context.Context, in db.CreateAlertRuleInput) (db.AlertRule, error) {
	f.created = &in
	return f.createResult, nil
}

func (f *fakeAlertStore) ListAlertRules(context.Context) ([]db.RuleWithSymbol, error) {
	return f.rules, nil
}

func (f *fakeAlertStore) DeleteAlertRule(_ context.Context, id int64) error {
	f.deletedID = id
	return f.deleteErr
}

func newAlertsServer(t *testing.T, store AlertStore, token string) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(Config{
		Reader:        &fakeReader{},
		Alerts:        store,
		OperatorToken: token,
		Logger:        logger,
		Registerer:    prometheus.NewRegistry(),
	}).Handler()
}

func alertRequest(method, target, body, token string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestAlertsRequireOperatorToken(t *testing.T) {
	h := newAlertsServer(t, &fakeAlertStore{}, testToken)

	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"wrong token", "not-the-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(h, alertRequest(http.MethodGet, "/api/v1/alerts", "", tc.token))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
		})
	}

	t.Run("valid token is accepted", func(t *testing.T) {
		rec := serve(h, alertRequest(http.MethodGet, "/api/v1/alerts", "", testToken))
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
	})
}

func TestAlertsLockedWhenNoTokenConfigured(t *testing.T) {
	// An empty configured token must fail closed: every request is rejected.
	h := newAlertsServer(t, &fakeAlertStore{}, "")
	rec := serve(h, alertRequest(http.MethodGet, "/api/v1/alerts", "", "anything"))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when no operator token is configured", rec.Code)
	}
}

func TestCreateAlert(t *testing.T) {
	store := &fakeAlertStore{
		instrumentID: 5,
		createResult: db.AlertRule{
			ID: 10, InstrumentID: 5, RuleType: db.RuleTypePriceBelow,
			Threshold: "25000.00000000", Channel: db.ChannelTelegram, Target: "123", IsEnabled: true,
		},
	}
	h := newAlertsServer(t, store, testToken)

	body := `{"symbol":"btcusdt","rule_type":"price_below","threshold":" 25000 ","channel":"telegram","target":"123"}`
	rec := serve(h, alertRequest(http.MethodPost, "/api/v1/alerts", body, testToken))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body)
	}
	if store.created == nil || store.created.InstrumentID != 5 || store.created.Threshold != "25000" {
		t.Errorf("stored input = %+v, want instrument 5 and trimmed threshold 25000", store.created)
	}
	var dto alertRuleDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.ID != 10 || dto.Symbol != "BTCUSDT" {
		t.Errorf("response = %+v, want id 10 and normalized symbol BTCUSDT", dto)
	}
}

func TestCreateAlertChangePct(t *testing.T) {
	store := &fakeAlertStore{instrumentID: 5, createResult: db.AlertRule{ID: 11}}
	h := newAlertsServer(t, store, testToken)

	body := `{"symbol":"BTCUSDT","rule_type":"change_pct","threshold":"5","window_seconds":300,"channel":"webhook","target":"https://x/y"}`
	rec := serve(h, alertRequest(http.MethodPost, "/api/v1/alerts", body, testToken))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", rec.Code, rec.Body)
	}
	if store.created == nil || store.created.WindowSeconds == nil || *store.created.WindowSeconds != 300 {
		t.Errorf("stored window = %v, want 300", store.created)
	}
}

func TestCreateAlertValidation(t *testing.T) {
	valid := `{"symbol":"BTCUSDT","rule_type":"price_below","threshold":"1","channel":"telegram","target":"x"}`
	cases := []struct {
		name     string
		body     string
		store    *fakeAlertStore
		wantCode int
	}{
		{"bad rule_type", `{"symbol":"BTCUSDT","rule_type":"nope","threshold":"1","channel":"telegram","target":"x"}`, &fakeAlertStore{instrumentID: 1}, http.StatusBadRequest},
		{"change_pct without window", `{"symbol":"BTCUSDT","rule_type":"change_pct","threshold":"5","channel":"telegram","target":"x"}`, &fakeAlertStore{instrumentID: 1}, http.StatusBadRequest},
		{"window on non-windowed rule", `{"symbol":"BTCUSDT","rule_type":"price_below","threshold":"1","window_seconds":60,"channel":"telegram","target":"x"}`, &fakeAlertStore{instrumentID: 1}, http.StatusBadRequest},
		{"non-numeric threshold", `{"symbol":"BTCUSDT","rule_type":"price_below","threshold":"abc","channel":"telegram","target":"x"}`, &fakeAlertStore{instrumentID: 1}, http.StatusBadRequest},
		{"bad channel", `{"symbol":"BTCUSDT","rule_type":"price_below","threshold":"1","channel":"sms","target":"x"}`, &fakeAlertStore{instrumentID: 1}, http.StatusBadRequest},
		{"empty target", `{"symbol":"BTCUSDT","rule_type":"price_below","threshold":"1","channel":"telegram","target":""}`, &fakeAlertStore{instrumentID: 1}, http.StatusBadRequest},
		{"unknown symbol", valid, &fakeAlertStore{instrumentErr: db.ErrNotFound}, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newAlertsServer(t, tc.store, testToken)
			rec := serve(h, alertRequest(http.MethodPost, "/api/v1/alerts", tc.body, testToken))
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.wantCode, rec.Body)
			}
		})
	}
}

func TestDeleteAlert(t *testing.T) {
	t.Run("existing rule is deleted", func(t *testing.T) {
		store := &fakeAlertStore{}
		h := newAlertsServer(t, store, testToken)
		rec := serve(h, alertRequest(http.MethodDelete, "/api/v1/alerts/42", "", testToken))
		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", rec.Code)
		}
		if store.deletedID != 42 {
			t.Errorf("deleted id = %d, want 42", store.deletedID)
		}
	})

	t.Run("unknown rule is 404", func(t *testing.T) {
		h := newAlertsServer(t, &fakeAlertStore{deleteErr: db.ErrNotFound}, testToken)
		rec := serve(h, alertRequest(http.MethodDelete, "/api/v1/alerts/42", "", testToken))
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("non-numeric id is 400", func(t *testing.T) {
		h := newAlertsServer(t, &fakeAlertStore{}, testToken)
		rec := serve(h, alertRequest(http.MethodDelete, "/api/v1/alerts/abc", "", testToken))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestListAlerts(t *testing.T) {
	window := int32(300)
	store := &fakeAlertStore{rules: []db.RuleWithSymbol{
		{AlertRule: db.AlertRule{ID: 1, RuleType: db.RuleTypePriceBelow, Threshold: "25000", Channel: db.ChannelTelegram, Target: "123", IsEnabled: true}, Symbol: "BTCUSDT"},
		{AlertRule: db.AlertRule{ID: 2, RuleType: db.RuleTypeChangePct, Threshold: "5", WindowSeconds: &window, Channel: db.ChannelWebhook, Target: "https://x/y", IsEnabled: false}, Symbol: "ETHUSDT"},
	}}
	h := newAlertsServer(t, store, testToken)

	rec := serve(h, alertRequest(http.MethodGet, "/api/v1/alerts", "", testToken))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body listAlertsDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Alerts) != 2 {
		t.Fatalf("alerts = %d, want 2", len(body.Alerts))
	}
	if body.Alerts[0].Symbol != "BTCUSDT" || body.Alerts[1].Symbol != "ETHUSDT" {
		t.Errorf("symbols = %q/%q", body.Alerts[0].Symbol, body.Alerts[1].Symbol)
	}
	if body.Alerts[1].WindowSeconds == nil || *body.Alerts[1].WindowSeconds != 300 {
		t.Errorf("window = %v, want 300", body.Alerts[1].WindowSeconds)
	}
}
