// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

import "errors"

// ErrCapacity is returned when capacity is not positive.
var ErrCapacity = errors.New("window: capacity must be > 0")

// Window retains the last capacity bytes written to it.
type Window struct {
	buf  []byte
	pos  int // next write index
	size int // number of valid bytes (<= cap)
}

// New creates a window of the given fixed capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns how many bytes are currently available.
func (w *Window) Len() int { return w.size }

// Reset empties the window.
func (w *Window) Reset() { w.pos, w.size = 0, 0 }

// Write appends p, dropping the oldest bytes past capacity.
func (w *Window) Write(p []byte) {
	for _, b := range p {
		w.buf[w.pos] = b
		w.pos++
		if w.pos == len(w.buf) {
			w.pos = 0
		}
		if w.size < len(w.buf) {
			w.size++
		}
	}
}

// At returns the byte at distance 1..Len() from the newest end.
// Distance 1 is the most recently written byte.
func (w *Window) At(distance int) byte {
	if distance < 1 || distance > w.size {
		panic("window: distance out of range")
	}
	idx := w.pos - distance
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}

// Tail returns up to n most recent bytes as a freshly allocated slice.
func (w *Window) Tail(n int) []byte {
	if n > w.size {
		n = w.size
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[n-1-i] = w.At(i + 1)
	}
	return out
}
