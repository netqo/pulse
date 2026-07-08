package processor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/domain"
)

var eventTime = time.Date(2025, time.June, 1, 12, 0, 0, 0, time.UTC)

// fakeStore assigns sequential instrument ids and records upsert calls.
type fakeStore struct {
	ids   map[string]int64
	next  int64
	calls map[string]int
}

func newFakeStore() *fakeStore {
	return &fakeStore{ids: map[string]int64{}, calls: map[string]int{}}
}

func (s *fakeStore) UpsertInstrument(_ context.Context, symbol string) (int64, error) {
	s.calls[symbol]++
	if id, ok := s.ids[symbol]; ok {
		return id, nil
	}
	s.next++
	s.ids[symbol] = s.next
	return s.next, nil
}

// fakeWriter captures written rows and can fail its first failures calls.
type fakeWriter struct {
	failures int
	calls    int
	rows     []db.PriceRow
}

func (w *fakeWriter) InsertPrices(_ context.Context, rows []db.PriceRow) (int64, error) {
	w.calls++
	if w.calls <= w.failures {
		return 0, errors.New("write unavailable")
	}
	w.rows = append(w.rows, rows...)
	return int64(len(rows)), nil
}

// fakeConsumer replays queued batches then cancels the run context so Run exits.
type fakeConsumer struct {
	batches [][][]byte
	idx     int
	commits int
	cancel  context.CancelFunc
}

func (c *fakeConsumer) Poll(context.Context) ([][]byte, error) {
	if c.idx >= len(c.batches) {
		c.cancel()
		return nil, context.Canceled
	}
	b := c.batches[c.idx]
	c.idx++
	return b, nil
}

func (c *fakeConsumer) Commit(context.Context) error {
	c.commits++
	return nil
}

func newTestProcessor(t *testing.T, store InstrumentStore, writer PriceWriter, windowSize int) *Processor {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(store, writer, logger, prometheus.NewRegistry(), windowSize)
}

func tickJSON(t *testing.T, symbol, price, qty string) []byte {
	t.Helper()
	b, err := json.Marshal(domain.Tick{Symbol: symbol, Price: price, Quantity: qty, EventTime: eventTime})
	if err != nil {
		t.Fatalf("marshal tick: %v", err)
	}
	return b
}

func TestEnrichFillsWindow(t *testing.T) {
	p := newTestProcessor(t, newFakeStore(), &fakeWriter{}, 2)

	first, err := p.enrich(context.Background(), tickJSON(t, "BTCUSDT", "100", "0.5"))
	if err != nil {
		t.Fatalf("enrich first: %v", err)
	}
	if first.InstrumentID != 1 {
		t.Errorf("InstrumentID = %d, want 1", first.InstrumentID)
	}
	if first.Price != "100" || first.Volume == nil || *first.Volume != "0.5" {
		t.Errorf("unexpected price/volume: %q %v", first.Price, first.Volume)
	}
	if first.MA20 != nil || first.Volatility != nil {
		t.Error("indicators must be nil until the window fills")
	}

	second, err := p.enrich(context.Background(), tickJSON(t, "BTCUSDT", "102", "0.5"))
	if err != nil {
		t.Fatalf("enrich second: %v", err)
	}
	if second.MA20 == nil || *second.MA20 != 101 {
		t.Errorf("MA20 = %v, want 101", second.MA20)
	}
	if second.Volatility == nil {
		t.Error("volatility should be set once the window is full")
	}
}

func TestEnrichRejectsBadPayloads(t *testing.T) {
	p := newTestProcessor(t, newFakeStore(), &fakeWriter{}, 20)

	if _, err := p.enrich(context.Background(), []byte("not json")); err == nil {
		t.Error("expected error on malformed JSON")
	}
	if _, err := p.enrich(context.Background(), tickJSON(t, "", "100", "1")); err == nil {
		t.Error("expected error on missing symbol")
	}
	if _, err := p.enrich(context.Background(), tickJSON(t, "BTCUSDT", "abc", "1")); err == nil {
		t.Error("expected error on non-numeric price")
	}

	zeroTime, err := json.Marshal(domain.Tick{Symbol: "BTCUSDT", Price: "100", Quantity: "1"})
	if err != nil {
		t.Fatalf("marshal zero-time tick: %v", err)
	}
	if _, err := p.enrich(context.Background(), zeroTime); err == nil {
		t.Error("expected error on zero event_time")
	}
}

func TestNewRejectsTinyWindow(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New should panic when windowSize < 2")
		}
	}()
	newTestProcessor(t, newFakeStore(), &fakeWriter{}, 1)
}

func TestRunWritesAndCommits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := newFakeStore()
	writer := &fakeWriter{}
	consumer := &fakeConsumer{
		cancel: cancel,
		batches: [][][]byte{
			{tickJSON(t, "BTCUSDT", "100", "1"), tickJSON(t, "ETHUSDT", "50", "2")},
			{tickJSON(t, "BTCUSDT", "101", "1")},
		},
	}
	p := newTestProcessor(t, store, writer, 20)

	if err := p.Run(ctx, consumer); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if len(writer.rows) != 3 {
		t.Errorf("wrote %d rows, want 3", len(writer.rows))
	}
	if consumer.commits != 2 {
		t.Errorf("committed %d times, want 2 (one per batch)", consumer.commits)
	}
	if store.calls["BTCUSDT"] != 1 {
		t.Errorf("BTCUSDT upserted %d times, want 1 (cached after first)", store.calls["BTCUSDT"])
	}
}

func TestRunRetriesWriteBeforeCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writer := &fakeWriter{failures: 1}
	consumer := &fakeConsumer{
		cancel:  cancel,
		batches: [][][]byte{{tickJSON(t, "BTCUSDT", "100", "1")}},
	}
	p := newTestProcessor(t, newFakeStore(), writer, 20)

	if err := p.Run(ctx, consumer); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if writer.calls != 2 {
		t.Errorf("InsertPrices called %d times, want 2 (one failure then success)", writer.calls)
	}
	if len(writer.rows) != 1 {
		t.Errorf("wrote %d rows, want 1", len(writer.rows))
	}
	// The offset is committed only after the retry succeeds.
	if consumer.commits != 1 {
		t.Errorf("committed %d times, want 1", consumer.commits)
	}
}

func TestRunSkipsMalformedButCommits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writer := &fakeWriter{}
	consumer := &fakeConsumer{
		cancel:  cancel,
		batches: [][][]byte{{[]byte("garbage"), tickJSON(t, "BTCUSDT", "100", "1")}},
	}
	p := newTestProcessor(t, newFakeStore(), writer, 20)

	if err := p.Run(ctx, consumer); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
	if len(writer.rows) != 1 {
		t.Errorf("wrote %d rows, want 1 (malformed skipped)", len(writer.rows))
	}
	if consumer.commits != 1 {
		t.Errorf("committed %d times, want 1", consumer.commits)
	}
	if got := testutil.ToFloat64(p.metrics.recordsSkipped); got != 1 {
		t.Errorf("records skipped = %v, want 1", got)
	}
}
