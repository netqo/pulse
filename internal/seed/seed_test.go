package seed

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/netqo/pulse/internal/db"
)

// fakeFetcher serves canned klines per month, or reports a month missing.
type fakeFetcher struct {
	byMonth map[string][]Kline
	missing map[string]bool
}

func (f *fakeFetcher) FetchMonth(_ context.Context, _, _ string, month time.Time) ([]Kline, error) {
	key := month.Format("2006-01")
	if f.missing[key] {
		return nil, ErrNoData
	}
	return f.byMonth[key], nil
}

type replaceCall struct {
	instrumentID int64
	from, to     time.Time
	rows         []db.PriceRow
}

// fakeStore records upserts and range replacements, and serves canned warm-up
// closes.
type fakeStore struct {
	ids      map[string]int64
	next     int64
	upserts  map[string]int
	replaces []replaceCall
	warm     []float64
}

func newFakeStore() *fakeStore {
	return &fakeStore{ids: map[string]int64{}, upserts: map[string]int{}}
}

func (s *fakeStore) ClosesBefore(_ context.Context, _ int64, _ time.Time, limit int) ([]float64, error) {
	if len(s.warm) > limit {
		return s.warm[len(s.warm)-limit:], nil
	}
	return s.warm, nil
}

func (s *fakeStore) UpsertInstrument(_ context.Context, symbol string) (int64, error) {
	s.upserts[symbol]++
	if id, ok := s.ids[symbol]; ok {
		return id, nil
	}
	s.next++
	s.ids[symbol] = s.next
	return s.next, nil
}

func (s *fakeStore) ReplacePricesInRange(_ context.Context, instrumentID int64, from, to time.Time, rows []db.PriceRow) (int64, error) {
	s.replaces = append(s.replaces, replaceCall{
		instrumentID: instrumentID,
		from:         from,
		to:           to,
		rows:         append([]db.PriceRow(nil), rows...),
	})
	return int64(len(rows)), nil
}

// makeKlines builds count klines starting at global index start (used to derive
// deterministic close prices), one minute apart from base.
func makeKlines(start, count int, base time.Time) []Kline {
	klines := make([]Kline, 0, count)
	for i := 0; i < count; i++ {
		v := float64(start + i + 1)
		klines = append(klines, Kline{
			OpenTime: base.Add(time.Duration(i) * time.Minute),
			Close:    strconv.FormatFloat(v, 'f', 8, 64),
			Volume:   "1.00000000",
		})
	}
	return klines
}

func newTestSeeder(t *testing.T, fetcher Fetcher, store Store) *Seeder {
	t.Helper()
	return New(fetcher, store, slog.New(slog.NewTextHandler(io.Discard, nil)), "1m", 20)
}

func TestSeedMultiMonth(t *testing.T) {
	jan := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	feb := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	fetcher := &fakeFetcher{byMonth: map[string][]Kline{
		"2024-01": makeKlines(0, 15, jan),
		"2024-02": makeKlines(15, 10, feb),
	}}
	store := newFakeStore()
	seeder := newTestSeeder(t, fetcher, store)

	summary, err := seeder.Seed(context.Background(), []string{"BTCUSDT"}, jan, feb)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if summary.Symbols != 1 || summary.Months != 2 || summary.Rows != 25 {
		t.Fatalf("summary = %+v, want {1, 2, 25}", summary)
	}
	if store.upserts["BTCUSDT"] != 1 {
		t.Errorf("upserts = %d, want 1", store.upserts["BTCUSDT"])
	}
	if len(store.replaces) != 2 {
		t.Fatalf("replace calls = %d, want 2", len(store.replaces))
	}

	// Each month is replaced over its own partition-aligned half-open range.
	if !store.replaces[0].from.Equal(jan) || !store.replaces[0].to.Equal(feb) {
		t.Errorf("Jan range = [%v, %v), want [%v, %v)", store.replaces[0].from, store.replaces[0].to, jan, feb)
	}
	mar := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	if !store.replaces[1].from.Equal(feb) || !store.replaces[1].to.Equal(mar) {
		t.Errorf("Feb range = [%v, %v), want [%v, %v)", store.replaces[1].from, store.replaces[1].to, feb, mar)
	}

	// First row carries the close price, volume and open time verbatim.
	first := store.replaces[0].rows[0]
	if first.Price != "1.00000000" || first.Volume == nil || *first.Volume != "1.00000000" {
		t.Errorf("first row price/volume = %q/%v, want 1.00000000", first.Price, first.Volume)
	}
	if !first.Ts.Equal(jan) {
		t.Errorf("first row ts = %v, want %v", first.Ts, jan)
	}

	// The window fills on the 20th overall kline (the 5th of February), so
	// ma_20 is nil before it and set from it on, proving continuity across the
	// month boundary.
	febRows := store.replaces[1].rows
	if febRows[3].MA20 != nil {
		t.Errorf("Feb row 3 ma_20 = %v, want nil (only 19 klines seen)", febRows[3].MA20)
	}
	if febRows[4].MA20 == nil || febRows[4].Volatility == nil {
		t.Error("Feb row 4 ma_20/volatility should be set (20 klines seen)")
	}
}

func TestSeedWarmsWindowFromPriorCloses(t *testing.T) {
	jan := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fetcher := &fakeFetcher{byMonth: map[string][]Kline{
		"2024-01": makeKlines(0, 3, jan),
	}}
	store := newFakeStore()
	// 19 prior closes warm the window, so the very first seeded kline completes
	// the 20-length window and carries a defined ma_20.
	store.warm = make([]float64, 19)
	for i := range store.warm {
		store.warm[i] = float64(i + 1)
	}
	seeder := newTestSeeder(t, fetcher, store)

	if _, err := seeder.Seed(context.Background(), []string{"BTCUSDT"}, jan, jan); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if len(store.replaces) != 1 {
		t.Fatalf("replace calls = %d, want 1", len(store.replaces))
	}
	if store.replaces[0].rows[0].MA20 == nil {
		t.Error("first seeded row should have ma_20 set thanks to window warm-up")
	}
}

func TestSeedSkipsMissingMonth(t *testing.T) {
	jan := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	feb := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	fetcher := &fakeFetcher{
		byMonth: map[string][]Kline{"2024-02": makeKlines(0, 3, feb)},
		missing: map[string]bool{"2024-01": true},
	}
	store := newFakeStore()
	seeder := newTestSeeder(t, fetcher, store)

	summary, err := seeder.Seed(context.Background(), []string{"BTCUSDT"}, jan, feb)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if summary.Months != 1 || summary.Rows != 3 {
		t.Errorf("summary = %+v, want months 1 rows 3", summary)
	}
	if len(store.replaces) != 1 || !store.replaces[0].from.Equal(feb) {
		t.Errorf("expected a single Feb replace, got %+v", store.replaces)
	}
	if store.upserts["BTCUSDT"] != 1 {
		t.Errorf("upserts = %d, want 1 even with a missing month", store.upserts["BTCUSDT"])
	}
}
