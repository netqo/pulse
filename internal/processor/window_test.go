package processor

import (
	"math"
	"testing"
)

func TestWindowNotFull(t *testing.T) {
	w := newWindow(3)
	w.add(10)
	w.add(20)

	if _, ok := w.mean(); ok {
		t.Error("mean should be undefined before the window fills")
	}
	if _, ok := w.volatility(); ok {
		t.Error("volatility should be undefined before the window fills")
	}
}

func TestWindowMeanAndVolatility(t *testing.T) {
	w := newWindow(4)
	for _, v := range []float64{2, 4, 4, 6} {
		w.add(v)
	}

	mean, ok := w.mean()
	if !ok {
		t.Fatal("mean should be defined once the window is full")
	}
	if mean != 4 {
		t.Errorf("mean = %v, want 4", mean)
	}

	vol, ok := w.volatility()
	if !ok {
		t.Fatal("volatility should be defined once the window is full")
	}
	// Sample stddev of {2,4,4,6}: variance = (4+0+0+4)/3 = 8/3.
	want := math.Sqrt(8.0 / 3.0)
	if math.Abs(vol-want) > 1e-9 {
		t.Errorf("volatility = %v, want %v", vol, want)
	}
}

func TestWindowSlides(t *testing.T) {
	w := newWindow(3)
	for _, v := range []float64{1, 2, 3, 4, 5} {
		w.add(v)
	}

	// The window now holds the last three prices {3,4,5}; mean = 4.
	mean, ok := w.mean()
	if !ok {
		t.Fatal("mean should be defined")
	}
	if mean != 4 {
		t.Errorf("mean after sliding = %v, want 4 (last three of 1..5)", mean)
	}
}
