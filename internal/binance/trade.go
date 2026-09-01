// Package binance parses and normalizes Binance market-stream messages into the
// canonical domain model.
package binance

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/netqo/pulse/internal/domain"
)

// combinedMessage is the envelope Binance uses on the combined stream endpoint
// (/stream?streams=...). On the raw endpoint (/ws) the payload arrives without
// this wrapper.
type combinedMessage struct {
	Data json.RawMessage `json:"data"`
}

// NormalizeTrade parses a Binance trade message (combined or raw) into a Tick.
// It returns an error for non-trade messages (for example subscription
// acknowledgements), which callers may treat as skippable.
//
// Fields are read from a case-sensitive key map rather than a struct because
// Binance trade payloads carry keys that differ only by case (e/E, t/T, m/M),
// and Go's struct-tag matching is case-insensitive, which would conflate them.
func NormalizeTrade(raw []byte) (domain.Tick, error) {
	var envelope combinedMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return domain.Tick{}, fmt.Errorf("binance: decode message: %w", err)
	}

	payload := raw
	if len(envelope.Data) > 0 {
		payload = envelope.Data // combined endpoint: unwrap the data field
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return domain.Tick{}, fmt.Errorf("binance: decode trade event: %w", err)
	}

	event, err := rawString(fields, "e")
	if err != nil {
		return domain.Tick{}, err
	}
	if event != "trade" {
		return domain.Tick{}, fmt.Errorf("binance: unexpected event type %q", event)
	}

	symbol, err := rawString(fields, "s")
	if err != nil {
		return domain.Tick{}, err
	}
	if symbol == "" {
		return domain.Tick{}, fmt.Errorf("binance: trade event missing symbol")
	}

	price, err := rawDecimalString(fields, "p")
	if err != nil {
		return domain.Tick{}, err
	}
	quantity, err := rawDecimalString(fields, "q")
	if err != nil {
		return domain.Tick{}, err
	}

	tradeID, err := rawInt64(fields, "t")
	if err != nil {
		return domain.Tick{}, err
	}
	tradeTime, err := rawInt64(fields, "T")
	if err != nil {
		return domain.Tick{}, err
	}

	return domain.Tick{
		Symbol:    symbol,
		Price:     price,
		Quantity:  quantity,
		TradeID:   tradeID,
		EventTime: time.UnixMilli(tradeTime).UTC(),
	}, nil
}

// rawString extracts a required string field.
func rawString(fields map[string]json.RawMessage, key string) (string, error) {
	v, ok := fields[key]
	if !ok {
		return "", fmt.Errorf("binance: missing field %q", key)
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", fmt.Errorf("binance: field %q: %w", key, err)
	}
	return s, nil
}

// rawDecimalString extracts a required decimal-string field and validates it
// parses as a number, keeping the original string to preserve precision.
func rawDecimalString(fields map[string]json.RawMessage, key string) (string, error) {
	s, err := rawString(fields, key)
	if err != nil {
		return "", err
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return "", fmt.Errorf("binance: field %q is not a number %q: %w", key, s, err)
	}
	return s, nil
}

// rawInt64 extracts a required integer field.
func rawInt64(fields map[string]json.RawMessage, key string) (int64, error) {
	v, ok := fields[key]
	if !ok {
		return 0, fmt.Errorf("binance: missing field %q", key)
	}
	var n int64
	if err := json.Unmarshal(v, &n); err != nil {
		return 0, fmt.Errorf("binance: field %q: %w", key, err)
	}
	return n, nil
}
