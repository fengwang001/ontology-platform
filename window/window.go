// Package window is a fixed-capacity ring buffer over the most
// recent bytes, addressed by absolute position or by distance
// back from the newest byte. It has no dependencies.
package window

import "errors"

// ErrCapacity is returned when a window is created with capacity < 1.
var ErrCapacity = errors.New("window: capacity must be positive")

// Window is a ring of the last Cap() appended bytes.
type Window struct {
	buf []byte
	pos int // absolute count of bytes appended so far
}

// New creates a window with the given capacity.
func New(capacity int) (*Window, error) {
	if capacity < 1 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the ring capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Pos returns the absolute count of bytes appended so far.
func (w *Window) Pos() int { return w.pos }

// Append adds one byte, evicting the oldest when full.
func (w *Window) Append(b byte) {
	w.buf[w.pos%len(w.buf)] = b
	w.pos++
}

// AppendBytes adds a slice of bytes in order.
func (w *Window) AppendBytes(p []byte) {
	for _, b := range p {
		w.Append(b)
	}
}

// At returns the byte at absolute position i. ok is false when i
// is negative, not yet written, or already evicted.
func (w *Window) At(i int) (b byte, ok bool) {
	if i < 0 || i >= w.pos || i < w.pos-len(w.buf) {
		return 0, false
	}
	return w.buf[i%len(w.buf)], true
}

// Last returns the byte at distance d (1 = most recent) back from
// the newest byte. ok is false when d < 1 or d exceeds what is held.
func (w *Window) Last(d int) (byte, bool) {
	if d < 1 {
		return 0, false
	}
	return w.At(w.pos - d)
}
