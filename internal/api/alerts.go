package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/netqo/pulse/internal/db"
)

// Alert request bounds. maxTargetChars mirrors the schema CHECK so a client gets
// a clear 400 rather than a 500 from a rejected write.
const (
	maxAlertBytes  = 8 << 10
	maxTargetChars = 2000
)

// alertRuleDTO is the wire representation of an alert rule. The threshold is a
// string to preserve exact decimal precision; window_seconds is present only for
// windowed (change_pct) rules.
type alertRuleDTO struct {
	ID            int64     `json:"id"`
	Symbol        string    `json:"symbol"`
	RuleType      string    `json:"rule_type"`
	Threshold     string    `json:"threshold"`
	WindowSeconds *int32    `json:"window_seconds,omitempty"`
	Channel       string    `json:"channel"`
	Target        string    `json:"target"`
	IsEnabled     bool      `json:"is_enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

// listAlertsDTO wraps the rule list in an object so the response can grow
// additional fields without breaking clients.
type listAlertsDTO struct {
	Alerts []alertRuleDTO `json:"alerts"`
}

// createAlertRequest is the JSON body of a create-rule request. The instrument is
// named by symbol; the handler resolves it to an id.
type createAlertRequest struct {
	Symbol        string `json:"symbol"`
	RuleType      string `json:"rule_type"`
	Threshold     string `json:"threshold"`
	WindowSeconds *int32 `json:"window_seconds"`
	Channel       string `json:"channel"`
	Target        string `json:"target"`
}

// handleListAlerts serves GET /api/v1/alerts: it lists every configured rule.
func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	rules, err := s.alerts.ListAlertRules(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]alertRuleDTO, 0, len(rules))
	for _, rule := range rules {
		out = append(out, ruleWithSymbolToDTO(rule))
	}
	writeJSON(w, http.StatusOK, listAlertsDTO{Alerts: out})
}

// handleCreateAlert serves POST /api/v1/alerts: it validates and persists a new
// rule, resolving the instrument by symbol.
func (s *Server) handleCreateAlert(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAlertBytes)
	var req createAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeClientError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	symbol := normalizeSymbol(req.Symbol)
	if msg, ok := validateCreateAlert(req, symbol); !ok {
		s.writeClientError(w, http.StatusBadRequest, msg)
		return
	}

	instrumentID, err := s.alerts.InstrumentIDBySymbol(r.Context(), symbol)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			s.writeClientError(w, http.StatusNotFound, fmt.Sprintf("unknown instrument %q", symbol))
			return
		}
		s.writeError(w, r, err)
		return
	}

	rule, err := s.alerts.CreateAlertRule(r.Context(), db.CreateAlertRuleInput{
		InstrumentID:  instrumentID,
		RuleType:      req.RuleType,
		Threshold:     strings.TrimSpace(req.Threshold),
		WindowSeconds: req.WindowSeconds,
		Channel:       req.Channel,
		Target:        req.Target,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, alertRuleToDTO(rule, symbol))
}

// handleDeleteAlert serves DELETE /api/v1/alerts/{id}: it removes a rule by id,
// returning 404 when no rule has that id.
func (s *Server) handleDeleteAlert(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.writeClientError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.alerts.DeleteAlertRule(r.Context(), id); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateCreateAlert enforces the request-level bounds and the rule-shape rules
// that mirror the schema CHECK constraints, so a bad request is a clear 400
// rather than a 500 from a rejected write. It returns a client-facing message and
// ok=false when the request is rejected.
func validateCreateAlert(req createAlertRequest, symbol string) (msg string, ok bool) {
	if symbol == "" {
		return "symbol is required", false
	}
	switch req.RuleType {
	case db.RuleTypePriceBelow, db.RuleTypePriceAbove, db.RuleTypeChangePct, db.RuleTypeCrosses:
	default:
		return "rule_type must be one of price_below, price_above, change_pct, crosses", false
	}
	threshold := strings.TrimSpace(req.Threshold)
	if threshold == "" {
		return "threshold is required", false
	}
	if _, err := strconv.ParseFloat(threshold, 64); err != nil {
		return "threshold must be a decimal number", false
	}
	if req.RuleType == db.RuleTypeChangePct {
		if req.WindowSeconds == nil || *req.WindowSeconds <= 0 {
			return "change_pct requires a positive window_seconds", false
		}
	} else if req.WindowSeconds != nil {
		return "window_seconds is only valid for change_pct rules", false
	}
	switch req.Channel {
	case db.ChannelTelegram, db.ChannelDiscord, db.ChannelWebhook:
	default:
		return "channel must be one of telegram, discord, webhook", false
	}
	if n := utf8.RuneCountInString(req.Target); n < 1 || n > maxTargetChars {
		return fmt.Sprintf("target must be between 1 and %d characters", maxTargetChars), false
	}
	return "", true
}

// ruleWithSymbolToDTO maps a stored rule (with its symbol) to its wire form.
func ruleWithSymbolToDTO(r db.RuleWithSymbol) alertRuleDTO {
	dto := alertRuleToDTO(r.AlertRule, r.Symbol)
	return dto
}

// alertRuleToDTO maps a stored rule and its symbol to the wire form.
func alertRuleToDTO(r db.AlertRule, symbol string) alertRuleDTO {
	return alertRuleDTO{
		ID:            r.ID,
		Symbol:        symbol,
		RuleType:      r.RuleType,
		Threshold:     r.Threshold,
		WindowSeconds: r.WindowSeconds,
		Channel:       r.Channel,
		Target:        r.Target,
		IsEnabled:     r.IsEnabled,
		CreatedAt:     r.CreatedAt.UTC(),
	}
}
