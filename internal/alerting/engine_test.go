package alerting

import (
	"strings"
	"testing"
	"time"

	"github.com/netqo/pulse/internal/db"
	"github.com/netqo/pulse/internal/domain"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func rule(id int64, symbol, ruleType, threshold string, window *int32) db.RuleWithSymbol {
	return db.RuleWithSymbol{
		AlertRule: db.AlertRule{
			ID:            id,
			RuleType:      ruleType,
			Threshold:     threshold,
			WindowSeconds: window,
			Channel:       db.ChannelWebhook,
			Target:        "https://example.com/hook",
			IsEnabled:     true,
		},
		Symbol: symbol,
	}
}

func tickAt(symbol, price string, offset time.Duration) domain.Tick {
	return domain.Tick{Symbol: symbol, Price: price, EventTime: base.Add(offset)}
}

// fireCount evaluates a tick and returns how many rules fired, failing on error.
func fireCount(t *testing.T, e *Evaluator, tk domain.Tick) int {
	t.Helper()
	firings, err := e.Eval(tk)
	if err != nil {
		t.Fatalf("Eval(%+v): %v", tk, err)
	}
	return len(firings)
}

func TestPriceBelowFiresOnEdge(t *testing.T) {
	e := NewEvaluator()
	e.SetRules([]db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil)})

	steps := []struct {
		price string
		want  int
	}{
		{"26000", 0}, // above threshold
		{"24000", 1}, // crosses below -> fires
		{"23000", 0}, // still below -> no re-fire
		{"26000", 0}, // back above -> re-arms
		{"24500", 1}, // below again -> fires
	}
	for i, s := range steps {
		if got := fireCount(t, e, tickAt("BTCUSDT", s.price, time.Duration(i)*time.Second)); got != s.want {
			t.Errorf("step %d price %s: fired %d, want %d", i, s.price, got, s.want)
		}
	}
}

func TestPriceAboveFiresOnEdge(t *testing.T) {
	e := NewEvaluator()
	e.SetRules([]db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceAbove, "30000", nil)})

	if got := fireCount(t, e, tickAt("BTCUSDT", "29000", 0)); got != 0 {
		t.Errorf("below: fired %d, want 0", got)
	}
	if got := fireCount(t, e, tickAt("BTCUSDT", "31000", time.Second)); got != 1 {
		t.Errorf("crossing above: fired %d, want 1", got)
	}
	if got := fireCount(t, e, tickAt("BTCUSDT", "32000", 2*time.Second)); got != 0 {
		t.Errorf("still above: fired %d, want 0", got)
	}
}

func TestCrossesFiresBothDirections(t *testing.T) {
	e := NewEvaluator()
	e.SetRules([]db.RuleWithSymbol{rule(1, "ETHUSDT", db.RuleTypeCrosses, "2000", nil)})

	steps := []struct {
		price string
		want  int
	}{
		{"1900", 0}, // first observation, no previous price -> cannot detect a cross
		{"2100", 1}, // crosses up
		{"2200", 0}, // stays above
		{"1950", 1}, // crosses down
		{"1900", 0}, // stays below
		{"2050", 1}, // crosses up again
	}
	for i, s := range steps {
		if got := fireCount(t, e, tickAt("ETHUSDT", s.price, time.Duration(i)*time.Second)); got != s.want {
			t.Errorf("step %d price %s: fired %d, want %d", i, s.price, got, s.want)
		}
	}
}

func TestChangePctFiresOverWindow(t *testing.T) {
	e := NewEvaluator()
	window := int32(60)
	e.SetRules([]db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypeChangePct, "5", &window)})

	// Not enough history spans the 60s window yet.
	if got := fireCount(t, e, tickAt("BTCUSDT", "100", 0)); got != 0 {
		t.Errorf("t=0: fired %d, want 0", got)
	}
	if got := fireCount(t, e, tickAt("BTCUSDT", "103", 30*time.Second)); got != 0 {
		t.Errorf("t=30 (+3%%, window not spanned): fired %d, want 0", got)
	}
	// t=70s vs the 100 at t=0 (the price ~60s ago) is +6% >= 5% -> fires.
	if got := fireCount(t, e, tickAt("BTCUSDT", "106", 70*time.Second)); got != 1 {
		t.Errorf("t=70 (+6%% over window): fired %d, want 1", got)
	}
	// Sustained above threshold does not re-fire.
	if got := fireCount(t, e, tickAt("BTCUSDT", "107", 80*time.Second)); got != 0 {
		t.Errorf("t=80 (still elevated): fired %d, want 0", got)
	}
}

func TestSetRulesPreservesStateAcrossRefresh(t *testing.T) {
	e := NewEvaluator()
	e.SetRules([]db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil)})

	if got := fireCount(t, e, tickAt("BTCUSDT", "24000", 0)); got != 1 {
		t.Fatalf("initial dip: fired %d, want 1", got)
	}

	// An unchanged rule reload must not reset the edge flag, or the next tick that
	// is still below the threshold would spuriously re-fire.
	e.SetRules([]db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil)})
	if got := fireCount(t, e, tickAt("BTCUSDT", "23000", time.Second)); got != 0 {
		t.Errorf("after refresh, still below: fired %d, want 0", got)
	}

	// Changing the threshold re-arms the rule.
	e.SetRules([]db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "22000", nil)})
	if got := fireCount(t, e, tickAt("BTCUSDT", "21000", 2*time.Second)); got != 1 {
		t.Errorf("after threshold change: fired %d, want 1", got)
	}
}

func TestEvalIgnoresOtherSymbolsAndBadPrices(t *testing.T) {
	e := NewEvaluator()
	e.SetRules([]db.RuleWithSymbol{rule(1, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil)})

	// A symbol with no rules yields no firings and no error, without parsing.
	if got := fireCount(t, e, tickAt("ETHUSDT", "not-a-number", 0)); got != 0 {
		t.Errorf("unmatched symbol: fired %d, want 0", got)
	}
	// A matched symbol with an unparseable price is an error the caller can skip.
	if _, err := e.Eval(tickAt("BTCUSDT", "not-a-number", time.Second)); err == nil {
		t.Error("Eval with bad price: err = nil, want an error")
	}
}

func TestRenderMessage(t *testing.T) {
	window := int32(300)
	cases := []struct {
		name    string
		firing  Firing
		wantSub []string
	}{
		{"below", Firing{Rule: rule(7, "BTCUSDT", db.RuleTypePriceBelow, "25000", nil), ObservedValue: "24000"}, []string{"BTCUSDT", "24000", "below", "25000", "#7"}},
		{"crosses", Firing{Rule: rule(8, "ETHUSDT", db.RuleTypeCrosses, "2000", nil), ObservedValue: "2100"}, []string{"ETHUSDT", "crossed", "2000", "2100", "#8"}},
		{"change", Firing{Rule: rule(9, "BTCUSDT", db.RuleTypeChangePct, "5", &window), ObservedValue: "26000"}, []string{"BTCUSDT", "5%", "300s", "26000", "#9"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := renderMessage(c.firing)
			for _, sub := range c.wantSub {
				if !strings.Contains(msg, sub) {
					t.Errorf("message %q missing %q", msg, sub)
				}
			}
		})
	}
}
