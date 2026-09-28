// Package window implements a fixed-capacity sliding window (ring buffer).
package window

import "errors"

// ErrCapacity is returned when the configured capacity is invalid.
var ErrCapacity = errors.New("window: capacity must be > 0")

// Window is a fixed-capacity ring buffer of the most recent bytes.
type Window struct {
	buf  []byte
	size int // number of valid bytes (<= cap)
	next int // index where the next byte is written
}

// New returns a window of the given capacity; capacity must be positive.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the window capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently held.
func (w *Window) Len() int { return w.size }

// Add appends p to the window, dropping the oldest bytes past capacity.
func (w *Window) Add(p []byte) {
	c := len(w.buf)
	if len(p) >= c {
		copy(w.buf, p[len(p)-c:])
		w.size, w.next = c, 0
		return
	}
	for _, b := range p {
		w.buf[w.next] = b
		w.next++
		if w.next == c {
			w.next = 0
		}
	}
	if w.size < c {
		if w.size+len(p) <= c {
			w.size += len(p)
		} else {
			w.size = c
		}
	}
}

// At returns the byte at distance from the newest end: distance 1 is the
// most recently added byte. distance must be in 1..Len().
func (w *Window) At(distance int) byte {
	c := len(w.buf)
	idx := w.next - distance
	for idx < 0 {
		idx += c
	}
	return w.buf[idx]
}
