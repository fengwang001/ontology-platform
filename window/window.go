// Package window is a fixed-capacity ring buffer of recent bytes.
package window

import "errors"

// ErrCapacity is returned when the configured capacity is not positive.
var ErrCapacity = errors.New("window: capacity must be positive")

// Window keeps at most cap most-recent bytes in a ring.
type Window struct {
	buf  []byte
	pos  int // index where the next byte is written
	size int // number of valid bytes, capped at cap(buf)
}

// New creates a window with the given fixed capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Reset empties the window without reallocating.
func (w *Window) Reset() { w.pos, w.size = 0, 0 }

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports the number of retained bytes (0..Cap).
func (w *Window) Len() int { return w.size }

// Add appends one byte, evicting the oldest byte when full.
func (w *Window) Add(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// Seed stores only the last min(len(p), Cap) bytes of p as a preset dictionary.
func (w *Window) Seed(p []byte) {
	if len(p) > len(w.buf) {
		p = p[len(p)-len(w.buf):]
	}
	for _, b := range p {
		w.Add(b)
	}
}

// At returns the byte at distance: At(1) is the most recently added byte.
func (w *Window) At(distance int) byte {
	idx := w.pos - distance
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}
