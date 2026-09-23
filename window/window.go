// Package window is a fixed-capacity ring buffer of recent bytes.
package window

import "errors"

// ErrInvalidConfig is returned for non-positive capacities.
var ErrInvalidConfig = errors.New("window: capacity must be positive")

// Window keeps the last capacity bytes written in insertion order.
type Window struct {
	buf []byte
	cap int
	pos int
	n   int
}

// New creates a Window of the given fixed capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Window{buf: make([]byte, capacity), cap: capacity}, nil
}

// WriteByte appends one byte, overwriting the oldest entry when full.
func (w *Window) WriteByte(c byte) {
	w.buf[w.pos] = c
	w.pos = (w.pos + 1) % w.cap
	if w.n < w.cap {
		w.n++
	}
}

// Len reports how many bytes are currently stored.
func (w *Window) Len() int { return w.n }

// At returns the byte at distance d from the newest byte (d=1 is newest).
func (w *Window) At(d int) byte {
	return w.buf[(w.pos-d+w.cap*2)%w.cap]
}
