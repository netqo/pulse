package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/netqo/pulse/internal/db"
)

// instrumentDTO is the wire representation of an instrument.
type instrumentDTO struct {
	Symbol     string `json:"symbol"`
	BaseAsset  string `json:"base_asset"`
	QuoteAsset string `json:"quote_asset"`
	Source     string `json:"source"`
	IsActive   bool   `json:"is_active"`
}

// listInstrumentsDTO wraps the instrument list in an object so the response can
// grow additional fields without breaking clients.
type listInstrumentsDTO struct {
	Instruments []instrumentDTO `json:"instruments"`
}

// pricePointDTO is the wire representation of a single price observation.
// Decimal fields are strings to preserve exact precision; optional ones are null
// when absent.
type pricePointDTO struct {
	Ts         time.Time `json:"ts"`
	Price      string    `json:"price"`
	Volume     *string   `json:"volume"`
	MA20       *string   `json:"ma_20"`
	Volatility *string   `json:"volatility"`
}

// latestPriceDTO flattens the price point next to its symbol.
type latestPriceDTO struct {
	Symbol string `json:"symbol"`
	pricePointDTO
}

// priceSeriesDTO is the response for a historical series query.
type priceSeriesDTO struct {
	Symbol string          `json:"symbol"`
	From   time.Time       `json:"from"`
	To     time.Time       `json:"to"`
	Count  int             `json:"count"`
	Prices []pricePointDTO `json:"prices"`
}

// errorDTO is the uniform error envelope.
type errorDTO struct {
	Error string `json:"error"`
}

// handleListInstruments serves GET /api/v1/instruments.
func (s *Server) handleListInstruments(w http.ResponseWriter, r *http.Request) {
	instruments, err := s.reader.ListInstruments(r.Context())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	out := make([]instrumentDTO, 0, len(instruments))
	for _, i := range instruments {
		out = append(out, instrumentDTO{
			Symbol:     i.Symbol,
			BaseAsset:  i.BaseAsset,
			QuoteAsset: i.QuoteAsset,
			Source:     i.Source,
			IsActive:   i.IsActive,
		})
	}
	writeJSON(w, http.StatusOK, listInstrumentsDTO{Instruments: out})
}

// handleLatestPrice serves GET /api/v1/instruments/{symbol}/latest.
func (s *Server) handleLatestPrice(w http.ResponseWriter, r *http.Request) {
	symbol, ok := s.requireSymbol(w, r)
	if !ok {
		return
	}
	point, err := s.reader.LatestPrice(r.Context(), symbol)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, latestPriceDTO{
		Symbol:        symbol,
		pricePointDTO: toPricePointDTO(point),
	})
}

// handlePriceSeries serves GET /api/v1/instruments/{symbol}/prices, accepting
// RFC3339 "from"/"to" bounds and an integer "limit".
func (s *Server) handlePriceSeries(w http.ResponseWriter, r *http.Request) {
	symbol, ok := s.requireSymbol(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	to, err := parseTime(q.Get("to"), time.Now())
	if err != nil {
		s.writeClientError(w, http.StatusBadRequest, "invalid 'to' timestamp: "+err.Error())
		return
	}
	from, err := parseTime(q.Get("from"), to.Add(-defaultSeriesWindow))
	if err != nil {
		s.writeClientError(w, http.StatusBadRequest, "invalid 'from' timestamp: "+err.Error())
		return
	}
	if !from.Before(to) {
		s.writeClientError(w, http.StatusBadRequest, "'from' must be before 'to'")
		return
	}
	limit, err := parseLimit(q.Get("limit"))
	if err != nil {
		s.writeClientError(w, http.StatusBadRequest, err.Error())
		return
	}

	points, err := s.reader.PriceSeries(r.Context(), symbol, from, to, limit)
	if err != nil {
		s.writeError(w, r, err)
		return
	}

	out := priceSeriesDTO{
		Symbol: symbol,
		From:   from.UTC(),
		To:     to.UTC(),
		Count:  len(points),
		Prices: make([]pricePointDTO, 0, len(points)),
	}
	for _, p := range points {
		out.Prices = append(out.Prices, toPricePointDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

// requireSymbol extracts and normalizes the {symbol} path parameter, writing a
// 400 and reporting false when it is missing.
func (s *Server) requireSymbol(w http.ResponseWriter, r *http.Request) (string, bool) {
	symbol := normalizeSymbol(r.PathValue("symbol"))
	if symbol == "" {
		s.writeClientError(w, http.StatusBadRequest, "symbol is required")
		return "", false
	}
	return symbol, true
}

// normalizeSymbol upper-cases and trims a path symbol so lookups are
// case-insensitive against the stored, upper-cased symbols.
func normalizeSymbol(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

// parseTime parses an RFC3339 timestamp, returning fallback when raw is empty.
func parseTime(raw string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	return time.Parse(time.RFC3339, raw)
}

// parseLimit parses the row limit, defaulting when absent and clamping to the
// maximum. A non-positive or non-integer value is a client error.
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return defaultSeriesLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid 'limit': %q is not an integer", raw)
	}
	if n <= 0 {
		return 0, errors.New("'limit' must be a positive integer")
	}
	if n > maxSeriesLimit {
		return maxSeriesLimit, nil
	}
	return n, nil
}

// toPricePointDTO maps a db.PricePoint to its wire form.
func toPricePointDTO(p db.PricePoint) pricePointDTO {
	return pricePointDTO{
		Ts:         p.Ts.UTC(),
		Price:      p.Price,
		Volume:     p.Volume,
		MA20:       p.MA20,
		Volatility: p.Volatility,
	}
}

// writeError maps a data-layer error to an HTTP response: ErrNotFound becomes a
// 404, a client-canceled request is logged quietly and not answered (the
// caller is already gone), and anything else a logged 500 with an opaque
// message.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		s.writeClientError(w, http.StatusNotFound, "not found")
	case errors.Is(err, context.Canceled):
		s.logger.Debug("request canceled by client", "method", r.Method, "path", r.URL.Path)
	default:
		s.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		s.writeClientError(w, http.StatusInternalServerError, "internal error")
	}
}

// writeClientError writes a JSON error envelope with the given status.
func (s *Server) writeClientError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorDTO{Error: msg})
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
