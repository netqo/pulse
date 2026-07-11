// Package alerting is the rule-evaluation stage of the pipeline. It consumes the
// same normalized tick stream as the processor, on its own Kafka consumer group,
// evaluates user-configured rules against each tick, and dispatches a
// notification (and an audit record) when a rule fires. Running as an independent
// consumer group is the core decoupling: a slow alert evaluation never blocks
// ingestion, and vice versa.
package alerting

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/domain"
)

// Firing is a rule that a tick triggered, carrying the observed price (as an
// exact decimal string) that triggered it for the audit record.
type Firing struct {
	Rule          db.EnabledRule
	ObservedValue string
}

// pricePoint is one observed price at a point in time, retained for windowed
// (change_pct) evaluation.
type pricePoint struct {
	ts    time.Time
	price float64
}

// ruleEval holds a rule together with the mutable state its evaluation needs:
// the previous price (for crosses), a bounded price history (for change_pct), and
// the edge flag that makes level conditions fire once per crossing rather than on
// every tick.
type ruleEval struct {
	rule      db.EnabledRule
	threshold float64
	window    time.Duration

	active    bool // condition satisfied on the last tick (for edge detection)
	havePrev  bool
	prevPrice float64
	history   []pricePoint
}

// Evaluator holds the enabled rules, indexed by symbol for fast tick matching,
// and their per-rule state. It is not safe for concurrent use: a single service
// goroutine drives SetRules and Eval sequentially.
type Evaluator struct {
	byID     map[int64]*ruleEval
	bySymbol map[string][]*ruleEval
}

// NewEvaluator returns an empty Evaluator with no rules.
func NewEvaluator() *Evaluator {
	return &Evaluator{
		byID:     make(map[int64]*ruleEval),
		bySymbol: make(map[string][]*ruleEval),
	}
}

// SetRules reconciles the active rule set to rules, preserving the accumulated
// state (price history, edge flag) of rules that are still present so a refresh
// does not cause spurious re-fires. Rules whose threshold cannot be parsed are
// dropped; the schema stores a valid NUMERIC, so this is a defensive guard.
func (e *Evaluator) SetRules(rules []db.EnabledRule) {
	next := make(map[int64]*ruleEval, len(rules))
	bySymbol := make(map[string][]*ruleEval)
	for _, r := range rules {
		threshold, err := strconv.ParseFloat(r.Threshold, 64)
		if err != nil {
			continue
		}
		ev := e.byID[r.ID]
		if ev == nil {
			ev = &ruleEval{}
		}
		ev.configure(r, threshold)
		next[r.ID] = ev
		bySymbol[r.Symbol] = append(bySymbol[r.Symbol], ev)
	}
	e.byID = next
	e.bySymbol = bySymbol
}

// configure updates a ruleEval's definition. When the condition parameters change
// the edge flag is reset so the rule re-arms; the price observations (prevPrice,
// history) stay valid because they are independent of the threshold.
func (ev *ruleEval) configure(r db.EnabledRule, threshold float64) {
	var window time.Duration
	if r.WindowSeconds != nil {
		window = time.Duration(*r.WindowSeconds) * time.Second
	}
	if ev.rule.RuleType != r.RuleType || ev.threshold != threshold || ev.window != window {
		ev.active = false
	}
	ev.rule = r
	ev.threshold = threshold
	ev.window = window
}

// Eval evaluates every rule for the tick's symbol and returns the ones that fire.
// It returns an error only when the tick price cannot be parsed; an empty slice
// (with a nil error) means no rule fired.
func (e *Evaluator) Eval(tick domain.Tick) ([]Firing, error) {
	evals := e.bySymbol[tick.Symbol]
	if len(evals) == 0 {
		return nil, nil
	}
	price, err := strconv.ParseFloat(tick.Price, 64)
	if err != nil {
		return nil, fmt.Errorf("alerting: parse tick price %q: %w", tick.Price, err)
	}

	var firings []Firing
	for _, ev := range evals {
		if ev.step(tick.EventTime, price) {
			firings = append(firings, Firing{Rule: ev.rule, ObservedValue: tick.Price})
		}
	}
	return firings, nil
}

// step advances a rule's state with a new observation and reports whether the
// rule fires on this tick.
func (ev *ruleEval) step(ts time.Time, price float64) bool {
	var fired bool
	switch ev.rule.RuleType {
	case db.RuleTypePriceBelow:
		fired = ev.edge(price < ev.threshold)
	case db.RuleTypePriceAbove:
		fired = ev.edge(price > ev.threshold)
	case db.RuleTypeChangePct:
		fired = ev.edge(ev.changeExceeds(ts, price))
	case db.RuleTypeCrosses:
		// A crossing is itself the edge, so it is not gated by the edge flag; each
		// time the price moves across the threshold the rule fires again.
		fired = ev.havePrev && crossed(ev.prevPrice, price, ev.threshold)
	}
	ev.prevPrice = price
	ev.havePrev = true
	return fired
}

// edge reports a firing only on the rising edge of a condition: it fires when the
// condition becomes satisfied and stays quiet until it clears and is satisfied
// again, so a level condition does not fire on every tick while it holds.
func (ev *ruleEval) edge(satisfied bool) bool {
	fired := satisfied && !ev.active
	ev.active = satisfied
	return fired
}

// changeExceeds records the observation and reports whether the absolute
// percentage change over the rule's window meets the threshold. It returns false
// until enough history spans the window.
func (ev *ruleEval) changeExceeds(ts time.Time, price float64) bool {
	ev.pushHistory(ts, price)
	past, ok := ev.referencePrice(ts.Add(-ev.window))
	if !ok || past == 0 {
		return false
	}
	change := math.Abs((price - past) / past * 100)
	return change >= ev.threshold
}

// pushHistory appends an observation and prunes points older than the window,
// keeping the newest such point as the reference for the "price a window ago"
// comparison.
func (ev *ruleEval) pushHistory(ts time.Time, price float64) {
	ev.history = append(ev.history, pricePoint{ts: ts, price: price})
	boundary := ts.Add(-ev.window)
	cut := 0
	for i, p := range ev.history {
		if p.ts.After(boundary) {
			break
		}
		cut = i
	}
	if cut > 0 {
		ev.history = ev.history[cut:]
	}
}

// referencePrice returns the most recent observed price at or before boundary,
// reporting false when no observation is that old yet.
func (ev *ruleEval) referencePrice(boundary time.Time) (float64, bool) {
	var ref float64
	var ok bool
	for _, p := range ev.history {
		if p.ts.After(boundary) {
			break
		}
		ref, ok = p.price, true
	}
	return ref, ok
}

// crossed reports whether the price moved from one side of threshold to the
// other between prev and cur, touching or passing the level.
func crossed(prev, cur, threshold float64) bool {
	return (prev < threshold && cur >= threshold) || (prev > threshold && cur <= threshold)
}

// renderMessage builds the human-readable notification text for a firing.
func renderMessage(f Firing) string {
	r := f.Rule
	switch r.RuleType {
	case db.RuleTypePriceBelow:
		return fmt.Sprintf("Pulse alert: %s price %s is below %s (rule #%d)", r.Symbol, f.ObservedValue, r.Threshold, r.ID)
	case db.RuleTypePriceAbove:
		return fmt.Sprintf("Pulse alert: %s price %s is above %s (rule #%d)", r.Symbol, f.ObservedValue, r.Threshold, r.ID)
	case db.RuleTypeCrosses:
		return fmt.Sprintf("Pulse alert: %s crossed %s, now %s (rule #%d)", r.Symbol, r.Threshold, f.ObservedValue, r.ID)
	case db.RuleTypeChangePct:
		var window int32
		if r.WindowSeconds != nil {
			window = *r.WindowSeconds
		}
		return fmt.Sprintf("Pulse alert: %s moved by at least %s%% over %ds, now %s (rule #%d)",
			r.Symbol, r.Threshold, window, f.ObservedValue, r.ID)
	default:
		return fmt.Sprintf("Pulse alert: %s rule #%d fired at %s", r.Symbol, r.ID, f.ObservedValue)
	}
}
