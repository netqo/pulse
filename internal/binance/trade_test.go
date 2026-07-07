package binance

import (
	"testing"
	"time"
)

func TestNormalizeTradeCombined(t *testing.T) {
	raw := []byte(`{"stream":"btcusdt@trade","data":{"e":"trade","E":1700000000000,"s":"BTCUSDT","t":42,"p":"65000.12345678","q":"0.005","T":1700000000123,"m":false}}`)

	tick, err := NormalizeTrade(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tick.Symbol != "BTCUSDT" {
		t.Errorf("Symbol = %q, want BTCUSDT", tick.Symbol)
	}
	if tick.Price != "65000.12345678" {
		t.Errorf("Price = %q, want 65000.12345678", tick.Price)
	}
	if tick.Quantity != "0.005" {
		t.Errorf("Quantity = %q, want 0.005", tick.Quantity)
	}
	if tick.TradeID != 42 {
		t.Errorf("TradeID = %d, want 42", tick.TradeID)
	}
	want := time.UnixMilli(1700000000123).UTC()
	if !tick.EventTime.Equal(want) {
		t.Errorf("EventTime = %v, want %v", tick.EventTime, want)
	}
	if tick.EventTime.Location() != time.UTC {
		t.Errorf("EventTime location = %v, want UTC", tick.EventTime.Location())
	}
}

func TestNormalizeTradeRaw(t *testing.T) {
	raw := []byte(`{"e":"trade","s":"ETHUSDT","t":7,"p":"3200.5","q":"1.25","T":1700000000000}`)

	tick, err := NormalizeTrade(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tick.Symbol != "ETHUSDT" || tick.Price != "3200.5" {
		t.Errorf("got %+v", tick)
	}
}

func TestNormalizeTradeErrors(t *testing.T) {
	tests := map[string]string{
		"invalid json":     `{not json`,
		"not a trade":      `{"stream":"x","data":{"e":"depthUpdate","s":"BTCUSDT"}}`,
		"missing symbol":   `{"e":"trade","s":"","p":"1","q":"1","T":1}`,
		"invalid price":    `{"e":"trade","s":"BTCUSDT","p":"abc","q":"1","T":1}`,
		"invalid quantity": `{"e":"trade","s":"BTCUSDT","p":"1","q":"xyz","T":1}`,
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeTrade([]byte(raw)); err == nil {
				t.Errorf("expected error for %q, got nil", raw)
			}
		})
	}
}
