// Package indicators provides the rolling market-data indicators shared across
// Pulse services: a fixed-size sliding window over recent prices and the moving
// average and volatility derived from it. The processor feeds it live ticks and
// the historical seeder feeds it archived closes, so both produce identical
// indicator semantics.
package indicators

import "math"

// Window is a fixed-size sliding window over the most recent prices of a single
// instrument. The moving average and volatility are only defined once the window
// has filled to its configured size, since a partial window would misrepresent
// them. Window is not safe for concurrent use.
type Window struct {
	size int
	buf  []float64
}

// NewWindow creates an empty window holding at most size prices. size must be at
// least 2, since a shorter window cannot define a moving average or a sample
// standard deviation.
func NewWindow(size int) *Window {
	if size < 2 {
		panic("indicators: window size must be >= 2")
	}
	return &Window{size: size, buf: make([]float64, 0, size)}
}

// Add records a new price, evicting the oldest once the window is full.
func (w *Window) Add(price float64) {
	if len(w.buf) < w.size {
		w.buf = append(w.buf, price)
		return
	}
	copy(w.buf, w.buf[1:])
	w.buf[w.size-1] = price
}

// Full reports whether the window holds a complete set of prices.
func (w *Window) Full() bool {
	return len(w.buf) == w.size
}

// Mean returns the moving average and true once the window is full; otherwise it
// returns false.
func (w *Window) Mean() (float64, bool) {
	if !w.Full() {
		return 0, false
	}
	var sum float64
	for _, v := range w.buf {
		sum += v
	}
	return sum / float64(w.size), true
}

// Volatility returns the sample standard deviation of the window and true once
// it is full; otherwise it returns false.
func (w *Window) Volatility() (float64, bool) {
	mean, ok := w.Mean()
	if !ok {
		return 0, false
	}
	var sumSquares float64
	for _, v := range w.buf {
		d := v - mean
		sumSquares += d * d
	}
	return math.Sqrt(sumSquares / float64(w.size-1)), true
}
