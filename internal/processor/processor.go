// Package processor consumes normalized ticks from Kafka, enriches each one with
// rolling per-instrument indicators (moving average and volatility) and persists
// the result into the partitioned prices table. It is the analytics stage of the
// pipeline, sitting between the fetcher and the read APIs.
package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/domain"
	"github.com/netqo/pulse/internal/kafka"
)

// InstrumentStore resolves a trading symbol to its instrument id, creating the
// instrument on first sight.
type InstrumentStore interface {
	UpsertInstrument(ctx context.Context, symbol string) (int64, error)
}

// PriceWriter bulk-persists enriched price rows, returning the number inserted.
type PriceWriter interface {
	InsertPrices(ctx context.Context, rows []db.PriceRow) (int64, error)
}

// Consumer delivers batches of raw tick payloads and commits their offsets once
// they have been processed, enabling at-least-once semantics.
type Consumer interface {
	Poll(ctx context.Context) ([][]byte, error)
	Commit(ctx context.Context) error
}

// retry bounds how long the processor waits between attempts to persist a batch
// while the database is unavailable, backing off from min to max.
const (
	minWriteBackoff = 200 * time.Millisecond
	maxWriteBackoff = 5 * time.Second
)

// Processor enriches ticks with rolling indicators and writes them to storage.
// A single instance drives one sequential consume loop, so its per-instrument
// state (windows, instrument cache) needs no synchronization.
type Processor struct {
	writer     PriceWriter
	instrument InstrumentStore
	logger     *slog.Logger
	metrics    *metrics

	windowSize    int
	windows       map[string]*window
	instrumentIDs map[string]int64
}

// metrics groups the Prometheus collectors the processor maintains.
type metrics struct {
	rowsWritten    prometheus.Counter
	recordsSkipped prometheus.Counter
	writeErrors    prometheus.Counter
	pollErrors     prometheus.Counter
	commitErrors   prometheus.Counter
	instruments    prometheus.Gauge
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		rowsWritten: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "processor_rows_written_total",
			Help: "Total number of enriched price rows written to storage.",
		}),
		recordsSkipped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "processor_records_skipped_total",
			Help: "Total number of records skipped because they could not be decoded.",
		}),
		writeErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "processor_write_errors_total",
			Help: "Total number of failed batch write attempts.",
		}),
		pollErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "processor_poll_errors_total",
			Help: "Total number of failed poll attempts against Kafka.",
		}),
		commitErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "processor_commit_errors_total",
			Help: "Total number of failed offset commits.",
		}),
		instruments: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "processor_instruments_tracked",
			Help: "Number of distinct instruments seen since startup.",
		}),
	}
	reg.MustRegister(m.rowsWritten, m.recordsSkipped, m.writeErrors, m.pollErrors, m.commitErrors, m.instruments)
	return m
}

// New constructs a Processor and registers its metrics with reg. The instrument
// store resolves instruments and writer persists rows; a single *db.DB satisfies
// both. windowSize must be at least 2, since a shorter window cannot define a
// moving average or a sample standard deviation.
func New(instrument InstrumentStore, writer PriceWriter, logger *slog.Logger, reg prometheus.Registerer, windowSize int) *Processor {
	if windowSize < 2 {
		panic(fmt.Sprintf("processor: windowSize must be >= 2, got %d", windowSize))
	}
	return &Processor{
		writer:        writer,
		instrument:    instrument,
		logger:        logger,
		metrics:       newMetrics(reg),
		windowSize:    windowSize,
		windows:       make(map[string]*window),
		instrumentIDs: make(map[string]int64),
	}
}

// Run consumes and processes batches until ctx is canceled or the consumer is
// closed, returning ctx.Err() on cancellation and nil on a clean shutdown.
func (p *Processor) Run(ctx context.Context, consumer Consumer) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		values, err := consumer.Poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, kafka.ErrClosed) {
				// The consumer was closed out from under us; stop cleanly
				// rather than spin polling a dead client.
				return nil
			}
			p.metrics.pollErrors.Inc()
			p.logger.Error("poll failed", "error", err)
			if stopped := sleep(ctx, minWriteBackoff); stopped {
				return ctx.Err()
			}
			continue
		}

		if err := p.processBatch(ctx, consumer, values); err != nil {
			// The only non-nil error is context cancellation during a write
			// retry; a clean shutdown surfaces as ctx.Err() at the loop top.
			return err
		}
	}
}

// processBatch enriches every value into a price row, persists the batch and
// then commits the consumed offsets. It returns an error only when ctx is
// canceled mid-write; malformed records are skipped, not fatal.
func (p *Processor) processBatch(ctx context.Context, consumer Consumer, values [][]byte) error {
	rows := make([]db.PriceRow, 0, len(values))
	for _, value := range values {
		row, err := p.enrich(ctx, value)
		if err != nil {
			p.metrics.recordsSkipped.Inc()
			p.logger.Warn("skipping record", "error", err)
			continue
		}
		rows = append(rows, row)
	}

	if len(rows) > 0 {
		if err := p.write(ctx, rows); err != nil {
			return err
		}
	}

	// Commit even when every record was skipped, so malformed payloads do not
	// wedge the partition; a write failure returns above before reaching here.
	// The commit is best-effort: a failure is recorded but not retried, since
	// the batch is already persisted and at-least-once tolerates the duplicate
	// rows a redelivery would produce.
	if err := consumer.Commit(ctx); err != nil {
		p.metrics.commitErrors.Inc()
		p.logger.Error("commit failed", "error", err)
	}
	return nil
}

// write persists rows, retrying with capped exponential backoff while the store
// is unavailable so no batch is dropped. It returns an error only if ctx is
// canceled while waiting to retry.
//
// Retries are unbounded by design: the expected failure is a transient database
// outage, which recovers. A genuinely poisonous batch (one that can never be
// written) would therefore stall the partition; introducing a dead-letter path
// for that case is deferred to a later phase.
func (p *Processor) write(ctx context.Context, rows []db.PriceRow) error {
	backoff := minWriteBackoff
	for {
		if _, err := p.writer.InsertPrices(ctx, rows); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			p.metrics.writeErrors.Inc()
			p.logger.Error("write failed, retrying", "error", err, "rows", len(rows), "backoff", backoff)
			if stopped := sleep(ctx, backoff); stopped {
				return ctx.Err()
			}
			backoff = nextBackoff(backoff)
			continue
		}
		p.metrics.rowsWritten.Add(float64(len(rows)))
		return nil
	}
}

// enrich decodes a raw tick payload, updates its rolling window and returns the
// enriched price row ready to persist.
func (p *Processor) enrich(ctx context.Context, value []byte) (db.PriceRow, error) {
	var tick domain.Tick
	if err := json.Unmarshal(value, &tick); err != nil {
		return db.PriceRow{}, fmt.Errorf("decode tick: %w", err)
	}
	if tick.Symbol == "" {
		return db.PriceRow{}, errors.New("tick missing symbol")
	}
	if tick.EventTime.IsZero() {
		// A zero timestamp would route to the year-0001 default partition and
		// silently poison the series, so reject it up front.
		return db.PriceRow{}, errors.New("tick missing event_time")
	}
	price, err := strconv.ParseFloat(tick.Price, 64)
	if err != nil {
		return db.PriceRow{}, fmt.Errorf("parse price %q: %w", tick.Price, err)
	}

	id, err := p.instrumentID(ctx, tick.Symbol)
	if err != nil {
		return db.PriceRow{}, err
	}

	w := p.windowFor(tick.Symbol)
	w.add(price)

	row := db.PriceRow{
		InstrumentID: id,
		Ts:           tick.EventTime,
		Price:        tick.Price,
	}
	if tick.Quantity != "" {
		volume := tick.Quantity
		row.Volume = &volume
	}
	if ma, ok := w.mean(); ok {
		row.MA20 = &ma
	}
	if vol, ok := w.volatility(); ok {
		row.Volatility = &vol
	}
	return row, nil
}

// instrumentID resolves symbol to its instrument id, caching the result so each
// instrument is upserted at most once per process lifetime.
func (p *Processor) instrumentID(ctx context.Context, symbol string) (int64, error) {
	if id, ok := p.instrumentIDs[symbol]; ok {
		return id, nil
	}
	id, err := p.instrument.UpsertInstrument(ctx, symbol)
	if err != nil {
		return 0, err
	}
	p.instrumentIDs[symbol] = id
	p.metrics.instruments.Set(float64(len(p.instrumentIDs)))
	return id, nil
}

// windowFor returns the sliding window for symbol, creating it on first use.
func (p *Processor) windowFor(symbol string) *window {
	w, ok := p.windows[symbol]
	if !ok {
		w = newWindow(p.windowSize)
		p.windows[symbol] = w
	}
	return w
}

// nextBackoff doubles the current backoff up to the configured ceiling.
func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > maxWriteBackoff {
		return maxWriteBackoff
	}
	return next
}

// sleep waits for d or until ctx is done, reporting whether ctx stopped it.
func sleep(ctx context.Context, d time.Duration) (stopped bool) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}
