// Package fetcher ingests live market data from Binance over a persistent
// WebSocket and produces normalized ticks to Kafka.
package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/binance"
)

// readLimit bounds a single WebSocket message to guard against unbounded reads.
const readLimit = 1 << 20 // 1 MiB

// Producer is the subset of the Kafka producer the fetcher depends on.
type Producer interface {
	Produce(ctx context.Context, key, value []byte) error
}

// Fetcher maintains a Binance WebSocket connection and produces ticks to Kafka,
// reconnecting with exponential backoff on failure.
type Fetcher struct {
	wsURL    string
	symbols  []string
	producer Producer
	logger   *slog.Logger
	metrics  *metrics

	// connected reflects whether the upstream WebSocket is currently live, read
	// by the readiness probe from another goroutine.
	connected atomic.Bool

	dialTimeout time.Duration
	minBackoff  time.Duration
	maxBackoff  time.Duration
}

// metrics groups the Prometheus collectors the fetcher maintains.
type metrics struct {
	ticksProduced prometheus.Counter
	produceErrors prometheus.Counter
	reconnects    prometheus.Counter
	connected     prometheus.Gauge
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		ticksProduced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fetcher_ticks_produced_total",
			Help: "Total number of normalized ticks produced to Kafka.",
		}),
		produceErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fetcher_produce_errors_total",
			Help: "Total number of failed produce attempts.",
		}),
		reconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "fetcher_reconnects_total",
			Help: "Total number of WebSocket reconnection attempts.",
		}),
		connected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "fetcher_connected",
			Help: "Whether the WebSocket is currently connected (1) or not (0).",
		}),
	}
	reg.MustRegister(m.ticksProduced, m.produceErrors, m.reconnects, m.connected)
	return m
}

// New constructs a Fetcher and registers its metrics with reg.
func New(wsURL string, symbols []string, producer Producer, logger *slog.Logger, reg prometheus.Registerer) *Fetcher {
	return &Fetcher{
		wsURL:       wsURL,
		symbols:     symbols,
		producer:    producer,
		logger:      logger,
		metrics:     newMetrics(reg),
		dialTimeout: 15 * time.Second,
		minBackoff:  time.Second,
		maxBackoff:  30 * time.Second,
	}
}

// Ready reports whether the fetcher currently holds a live upstream connection.
// It backs the readiness probe and is safe to call from any goroutine.
func (f *Fetcher) Ready() bool {
	return f.connected.Load()
}

// Run connects and streams until ctx is canceled, reconnecting with backoff.
// It returns ctx.Err() on cancellation.
func (f *Fetcher) Run(ctx context.Context) error {
	endpoint, err := f.streamURL()
	if err != nil {
		return err
	}

	backoff := f.minBackoff
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		connected, streamErr := f.stream(ctx, endpoint)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// A healthy connection that dropped should reconnect promptly; only a
		// failure to establish a connection warrants exponential backoff.
		f.metrics.reconnects.Inc()
		if connected {
			backoff = f.minBackoff
		}
		f.logger.Warn("stream disconnected; reconnecting",
			"error", streamErr, "connected", connected, "backoff", backoff.String())

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		if !connected {
			backoff = nextBackoff(backoff, f.maxBackoff)
		}
	}
}

// stream opens one WebSocket connection and reads until it errors or ctx ends.
// It reports whether a connection was successfully established, which the caller
// uses to decide the backoff strategy.
func (f *Fetcher) stream(ctx context.Context, endpoint string) (connected bool, err error) {
	dialCtx, cancel := context.WithTimeout(ctx, f.dialTimeout)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(readLimit)

	f.setConnected(true)
	defer f.setConnected(false)
	f.logger.Info("connected to binance stream", "symbols", f.symbols)

	for {
		_, data, readErr := conn.Read(ctx)
		if readErr != nil {
			return true, fmt.Errorf("read: %w", readErr)
		}
		f.handleMessage(ctx, data)
	}
}

// setConnected updates both the readiness flag and the connection gauge.
func (f *Fetcher) setConnected(v bool) {
	f.connected.Store(v)
	if v {
		f.metrics.connected.Set(1)
	} else {
		f.metrics.connected.Set(0)
	}
}

// nextBackoff doubles current, capped at ceiling.
func nextBackoff(current, ceiling time.Duration) time.Duration {
	next := current * 2
	if next > ceiling {
		return ceiling
	}
	return next
}

// handleMessage normalizes one raw message and produces it. Non-trade messages
// (for example subscription acknowledgements) are skipped, not fatal.
func (f *Fetcher) handleMessage(ctx context.Context, data []byte) {
	tick, err := binance.NormalizeTrade(data)
	if err != nil {
		f.logger.Debug("skipping message", "error", err)
		return
	}

	payload, err := json.Marshal(tick)
	if err != nil {
		f.logger.Error("marshal tick", "symbol", tick.Symbol, "error", err)
		return
	}

	if err := f.producer.Produce(ctx, []byte(tick.Symbol), payload); err != nil {
		f.metrics.produceErrors.Inc()
		f.logger.Error("produce tick", "symbol", tick.Symbol, "error", err)
		return
	}
	f.metrics.ticksProduced.Inc()
}

// streamURL builds the Binance combined-stream endpoint for the symbols. The
// query is assembled manually because Binance expects literal '/' and '@'
// separators, which standard query encoding would percent-escape.
func (f *Fetcher) streamURL() (string, error) {
	if len(f.symbols) == 0 {
		return "", errors.New("fetcher: no symbols configured")
	}
	if _, err := url.Parse(f.wsURL); err != nil {
		return "", fmt.Errorf("fetcher: invalid ws url: %w", err)
	}

	streams := make([]string, 0, len(f.symbols))
	for _, s := range f.symbols {
		streams = append(streams, strings.ToLower(s)+"@trade")
	}
	return fmt.Sprintf("%s?streams=%s",
		strings.TrimRight(f.wsURL, "/"), strings.Join(streams, "/")), nil
}
