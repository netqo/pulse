// Package domain holds the core types shared across Pulse services.
package domain

import "time"

// Tick is the canonical, normalized market event that the Fetcher produces to
// Kafka and downstream services consume. Monetary fields are kept as decimal
// strings to preserve the exchange's exact precision until they are persisted
// into the NUMERIC columns of the prices table.
type Tick struct {
	// Symbol is the instrument symbol, e.g. "BTCUSDT".
	Symbol string `json:"symbol"`
	// Price is the trade price as a decimal string.
	Price string `json:"price"`
	// Quantity is the traded quantity as a decimal string.
	Quantity string `json:"quantity"`
	// TradeID is the exchange-assigned trade identifier.
	TradeID int64 `json:"trade_id"`
	// EventTime is the exchange event time in UTC.
	EventTime time.Time `json:"event_time"`
}
