package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/domain"
	"github.com/netqo/pulse/internal/kafka"
)

// fakeConsumer replays batches then reports the consumer as closed, so Run exits
// cleanly after the fixture is drained.
type fakeConsumer struct {
	batches [][][]byte
	idx     int
	commits int
}

func (c *fakeConsumer) Poll(context.Context) ([][]byte, error) {
	if c.idx >= len(c.batches) {
		return nil, kafka.ErrClosed
	}
	b := c.batches[c.idx]
	c.idx++
	return b, nil
}

func (c *fakeConsumer) Commit(context.Context) error {
	c.commits++
	return nil
}

type fakeStore struct {
	rules []db.RuleWithSymbol
	err   error
}

func (s *fakeStore) EnabledAlertRules(context.Context) ([]db.RuleWithSymbol, error) {
	return s.rules, s.err
}

type dispatchCall struct{ channel, target, message string }

type fakeNotifier struct {
	calls []dispatchCall
	err   error
}

func (n *fakeNotifier) Dispatch(_ context.Context, channel, target, message string) error {
	n.calls = append(n.calls, dispatchCall{channel, target, message})
	return n.err
}

type fakeHistory struct {
	records []db.AlertHistoryInput
	err     error
}

func (h *fakeHistory) RecordAlert(_ context.Context, in db.AlertHistoryInput) (db.AlertHistory, error) {
	h.records = append(h.records, in)
	return db.AlertHistory{ID: int64(len(h.records))}, h.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func tickJSON(t *testing.T, tk domain.Tick) []byte {
	t.Helper()
	b, err := json.Marshal(tk)
	if err != nil {
		t.Fatalf("marshal tick: %v", err)
	}
	return b
}

func TestServiceDeliversAndRecords(t *testing.T) {
	store := &fakeStore{rules: []db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil)}}
	notifier := &fakeNotifier{}
	history := &fakeHistory{}
	consumer := &fakeConsumer{batches: [][][]byte{{tickJSON(t, tickAt("BTCUSDT", "24000", 0))}}}

	svc := New(store, history, notifier, discardLogger(), prometheus.NewRegistry(), time.Hour)
	if err := svc.Run(context.Background(), consumer); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(notifier.calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1", len(notifier.calls))
	}
	call := notifier.calls[0]
	if call.channel != db.ChannelWebhook || call.target != "https://example.com/hook" {
		t.Errorf("dispatched to %s/%s, want webhook/hook URL", call.channel, call.target)
	}
	if !strings.Contains(call.message, "BTCUSDT") || !strings.Contains(call.message, "below") {
		t.Errorf("message = %q, want it to describe the BTCUSDT below-threshold alert", call.message)
	}
	if len(history.records) != 1 {
		t.Fatalf("history records = %d, want 1", len(history.records))
	}
	rec := history.records[0]
	if rec.RuleID != 1 || rec.ObservedValue != "24000" || rec.DeliveryStatus != db.DeliverySent {
		t.Errorf("history record = %+v, want rule 1, 24000, sent", rec)
	}
	if consumer.commits == 0 {
		t.Error("offsets were never committed")
	}
}

func TestServiceRecordsFailedDelivery(t *testing.T) {
	store := &fakeStore{rules: []db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil)}}
	notifier := &fakeNotifier{err: errors.New("channel down")}
	history := &fakeHistory{}
	consumer := &fakeConsumer{batches: [][][]byte{{tickJSON(t, tickAt("BTCUSDT", "24000", 0))}}}

	svc := New(store, history, notifier, discardLogger(), prometheus.NewRegistry(), time.Hour)
	if err := svc.Run(context.Background(), consumer); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// A failed delivery is still attempted and recorded, with a failed status.
	if len(notifier.calls) != 1 {
		t.Errorf("dispatch calls = %d, want 1 (attempted)", len(notifier.calls))
	}
	if len(history.records) != 1 || history.records[0].DeliveryStatus != db.DeliveryFailed {
		t.Errorf("history records = %+v, want one with status failed", history.records)
	}
}

func TestServiceSkipsMalformedRecords(t *testing.T) {
	store := &fakeStore{rules: []db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil)}}
	notifier := &fakeNotifier{}
	history := &fakeHistory{}
	consumer := &fakeConsumer{batches: [][][]byte{{
		[]byte("not json"),
		tickJSON(t, tickAt("BTCUSDT", "24000", 0)),
	}}}

	svc := New(store, history, notifier, discardLogger(), prometheus.NewRegistry(), time.Hour)
	if err := svc.Run(context.Background(), consumer); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The malformed record is skipped; the valid tick still fires and commits.
	if len(notifier.calls) != 1 {
		t.Errorf("dispatch calls = %d, want 1", len(notifier.calls))
	}
	if consumer.commits == 0 {
		t.Error("batch with a malformed record never committed")
	}
}
