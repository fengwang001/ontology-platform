// Package window is a fixed-capacity ring buffer of emitted bytes.
package window

import "errors"

// ErrInvalidConfig is returned when the capacity is not positive.
var ErrInvalidConfig = errors.New("window: capacity must be > 0")

// Window keeps the most recent Cap bytes in insertion order. The zero value is
// not usable; construct with New.
type Window struct {
	buf  []byte
	mask int
	pos  int // number of bytes ever pushed
}

// New creates a window whose capacity is rounded up to a power of two.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrInvalidConfig
	}
	c := 1
	for c < capacity {
		c <<= 1
	}
	return &Window{buf: make([]byte, c), mask: c - 1}, nil
}

// Cap reports the (power-of-two) capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports how many bytes are currently stored (<= Cap).
func (w *Window) Len() int {
	if w.pos < len(w.buf) {
		return w.pos
	}
	return len(w.buf)
}

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(b byte) {
	w.buf[w.pos&w.mask] = b
	w.pos++
}

// PushN appends a slice.
func (w *Window) PushN(p []byte) {
	for _, b := range p {
		w.Push(b)
	}
}

// ByteAt returns the byte at distance d from the newest position (d=1 is the
// most recently pushed byte).
func (w *Window) ByteAt(d int) byte {
	return w.buf[(w.pos-d)&w.mask]
}

// Prefill seeds the window with dictionary bytes (used by parallel blocks).
// Only the last Cap bytes are retained; search history starts empty.
func (w *Window) Prefill(dict []byte) {
	if len(dict) > len(w.buf) {
		dict = dict[len(dict)-len(w.buf):]
	}
	w.pos = 0
	w.PushN(dict)
}

// Tail returns up to n most recent bytes (a block dictionary for the next block).
func (w *Window) Tail(n int) []byte {
	if n > w.Len() {
		n = w.Len()
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[n-1-i] = w.ByteAt(i + 1)
	}
	return out
}
