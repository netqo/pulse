package processor

import "math"

// window is a fixed-size sliding window over the most recent prices of a single
// instrument. It backs the moving average and volatility indicators, which are
// only defined once the window has filled to its configured size.
type window struct {
	size int
	buf  []float64
}

// newWindow creates an empty window holding at most size prices.
func newWindow(size int) *window {
	return &window{size: size, buf: make([]float64, 0, size)}
}

// add records a new price, evicting the oldest once the window is full.
func (w *window) add(price float64) {
	if len(w.buf) < w.size {
		w.buf = append(w.buf, price)
		return
	}
	copy(w.buf, w.buf[1:])
	w.buf[w.size-1] = price
}

// full reports whether the window holds a complete set of prices.
func (w *window) full() bool {
	return len(w.buf) == w.size
}

// mean returns the moving average and true once the window is full; otherwise it
// returns false, since a partial window would misrepresent the indicator.
func (w *window) mean() (float64, bool) {
	if !w.full() {
		return 0, false
	}
	var sum float64
	for _, v := range w.buf {
		sum += v
	}
	return sum / float64(w.size), true
}

// volatility returns the sample standard deviation of the window and true once
// it is full; otherwise it returns false.
func (w *window) volatility() (float64, bool) {
	mean, ok := w.mean()
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
