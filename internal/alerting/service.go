package alerting

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/domain"
	"github.com/netqo/pulse/internal/kafka"
)

// Consumer delivers batches of raw tick payloads and commits their offsets once
// processed, enabling at-least-once consumption on the alerting consumer group.
type Consumer interface {
	Poll(ctx context.Context) ([][]byte, error)
	Commit(ctx context.Context) error
}

// RuleStore loads the enabled rules the service evaluates.
type RuleStore interface {
	EnabledAlertRules(ctx context.Context) ([]db.RuleWithSymbol, error)
}

// HistoryRecorder persists a fired-alert audit record.
type HistoryRecorder interface {
	RecordAlert(ctx context.Context, in db.AlertHistoryInput) (db.AlertHistory, error)
}

// Notifier delivers a rendered message to a channel/target. *notify.Dispatcher
// satisfies it.
type Notifier interface {
	Dispatch(ctx context.Context, channel, target, message string) error
}

// pollBackoff bounds the wait between failed Kafka polls.
const pollBackoff = 500 * time.Millisecond

// Service consumes ticks, evaluates alert rules and dispatches notifications.
// A single instance drives one sequential consume loop, so the evaluator and the
// refresh bookkeeping need no synchronization.
type Service struct {
	rules     RuleStore
	history   HistoryRecorder
	notifier  Notifier
	evaluator *Evaluator
	logger    *slog.Logger
	metrics   *metrics

	refreshEvery time.Duration
	lastRefresh  time.Time
}

// New constructs a Service. rules and history are typically the same *db.DB;
// notifier is a configured *notify.Dispatcher. refreshEvery bounds how stale the
// in-memory rule set may be relative to the database.
func New(rules RuleStore, history HistoryRecorder, notifier Notifier, logger *slog.Logger, reg prometheus.Registerer, refreshEvery time.Duration) *Service {
	return &Service{
		rules:        rules,
		history:      history,
		notifier:     notifier,
		evaluator:    NewEvaluator(),
		logger:       logger,
		metrics:      newMetrics(reg),
		refreshEvery: refreshEvery,
	}
}

// Run consumes and evaluates batches until ctx is canceled or the consumer is
// closed, returning ctx.Err() on cancellation and nil on a clean shutdown.
func (s *Service) Run(ctx context.Context, consumer Consumer) error {
	// Load rules once before the first batch so the service alerts from its first
	// tick; a failure here is logged and retried on the refresh cadence.
	s.refreshRules(ctx)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.maybeRefresh(ctx)

		values, err := consumer.Poll(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, kafka.ErrClosed) {
				return nil
			}
			s.metrics.pollErrors.Inc()
			s.logger.Error("poll failed", "error", err)
			if stopped := sleep(ctx, pollBackoff); stopped {
				return ctx.Err()
			}
			continue
		}

		s.processBatch(ctx, consumer, values)
	}
}

// processBatch evaluates every tick in the batch, dispatches any firings, and
// commits the consumed offsets. Delivery is best-effort: a failed notification or
// audit write is recorded and logged but never blocks the commit, so a slow or
// unavailable channel cannot wedge the partition.
func (s *Service) processBatch(ctx context.Context, consumer Consumer, values [][]byte) {
	for _, value := range values {
		var tick domain.Tick
		if err := json.Unmarshal(value, &tick); err != nil {
			s.metrics.recordsSkipped.Inc()
			s.logger.Warn("skipping record", "error", err)
			continue
		}
		firings, err := s.evaluator.Eval(tick)
		if err != nil {
			s.metrics.recordsSkipped.Inc()
			s.logger.Warn("skipping tick", "symbol", tick.Symbol, "error", err)
			continue
		}
		for _, f := range firings {
			s.handleFiring(ctx, f)
		}
	}

	if err := consumer.Commit(ctx); err != nil {
		s.metrics.commitErrors.Inc()
		s.logger.Error("commit failed", "error", err)
	}
}

// handleFiring dispatches the notification for a firing and records the outcome
// in the audit history. Both steps are independent and best-effort; a failure in
// one is logged and metered without aborting the other.
func (s *Service) handleFiring(ctx context.Context, f Firing) {
	s.metrics.alertsFired.Inc()
	message := renderMessage(f)

	status := db.DeliverySent
	if err := s.notifier.Dispatch(ctx, f.Rule.Channel, f.Rule.Target, message); err != nil {
		status = db.DeliveryFailed
		s.metrics.deliveryErrors.Inc()
		s.logger.Error("alert delivery failed", "rule", f.Rule.ID, "channel", f.Rule.Channel, "error", err)
	} else {
		s.metrics.alertsDelivered.Inc()
		s.logger.Info("alert delivered", "rule", f.Rule.ID, "symbol", f.Rule.Symbol, "channel", f.Rule.Channel)
	}

	if _, err := s.history.RecordAlert(ctx, db.AlertHistoryInput{
		RuleID:         f.Rule.ID,
		ObservedValue:  f.ObservedValue,
		DeliveryStatus: status,
	}); err != nil {
		s.metrics.historyErrors.Inc()
		s.logger.Error("recording alert history failed", "rule", f.Rule.ID, "error", err)
	}
}

// maybeRefresh reloads the rule set when the refresh interval has elapsed.
func (s *Service) maybeRefresh(ctx context.Context) {
	if time.Since(s.lastRefresh) < s.refreshEvery {
		return
	}
	s.refreshRules(ctx)
}

// refreshRules loads the enabled rules and hands them to the evaluator. On
// failure it keeps the current rule set and retries on the next cadence, so a
// transient database blip does not silently stop alerting. lastRefresh is stamped
// regardless so failures back off to the refresh interval rather than every tick.
func (s *Service) refreshRules(ctx context.Context) {
	s.lastRefresh = time.Now()
	rules, err := s.rules.EnabledAlertRules(ctx)
	if err != nil {
		s.metrics.refreshErrors.Inc()
		s.logger.Error("loading alert rules failed", "error", err)
		return
	}
	s.evaluator.SetRules(rules)
	s.metrics.rulesLoaded.Set(float64(len(rules)))
	s.logger.Debug("alert rules loaded", "count", len(rules))
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
