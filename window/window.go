// Package window is a fixed-capacity byte ring buffer used as the LZ77
// history window. It depends on no other package.
package window

import "errors"

// ErrCapacity is returned when a non-positive capacity is requested.
var ErrCapacity = errors.New("window: capacity must be positive")

// Window stores at most Cap most recently written bytes in a ring.
// A Window is not safe for concurrent use.
type Window struct {
	buf  []byte
	next int
	len  int
}

// New creates a Window with the given fixed capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently retained (<= Cap).
func (w *Window) Len() int { return w.len }

// Reset empties the window.
func (w *Window) Reset() {
	w.next, w.len = 0, 0
}

// WriteByte appends one byte, overwriting the oldest byte when full.
func (w *Window) WriteByte(b byte) {
	w.buf[w.next] = b
	w.next++
	if w.next == len(w.buf) {
		w.next = 0
	}
	if w.len < len(w.buf) {
		w.len++
	}
}

// At returns the byte at the given distance from the newest byte:
// At(1) is the most recently written byte. dist must satisfy 1 <= dist <= Len.
func (w *Window) At(dist int) byte {
	i := w.next - dist
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}
