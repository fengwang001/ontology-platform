// Package window is a fixed-capacity ring buffer of emitted bytes.
// It depends on no other package.
package window

import "errors"

// ErrCapacity is returned when a non-positive capacity is configured.
var ErrCapacity = errors.New("window: capacity must be positive")

// Window is a fixed-capacity ring buffer. Distances count backwards from the
// newest appended byte: distance 1 is the most recently written byte.
type Window struct {
	buf []byte
	// head is the index where the next byte will be written.
	head int
	// size is the number of valid bytes (<= cap(buf)).
	size int
}

// New creates a Window of the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the configured capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently held.
func (w *Window) Len() int { return w.size }

// Reset empties the window.
func (w *Window) Reset() { w.head, w.size = 0, 0 }

// AppendAll appends every byte of p, wrapping and overwriting the oldest data.
func (w *Window) AppendAll(p []byte) {
	for _, c := range p {
		w.buf[w.head] = c
		w.head++
		if w.head == len(w.buf) {
			w.head = 0
		}
		if w.size < len(w.buf) {
			w.size++
		}
	}
}

// At returns the byte at the given distance (1 = most recent byte).
func (w *Window) At(dist int) byte {
	i := w.head - dist
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

// Slice returns up to maxLen historical bytes starting at distance dist,
// oldest first. It returns fewer than maxLen only near buffer boundaries.
func (w *Window) Slice(dist, maxLen int) []byte {
	if dist > w.size || dist < 1 {
		return nil
	}
	n := dist
	if n > maxLen {
		n = maxLen
	}
	start := w.head - dist
	if start < 0 {
		start += len(w.buf)
	}
	out := make([]byte, n)
	for i := range out {
		idx := start + i
		if idx >= len(w.buf) {
			idx -= len(w.buf)
		}
		out[i] = w.buf[idx]
	}
	return out
}
