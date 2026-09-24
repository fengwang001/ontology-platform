// Package window is a fixed-capacity ring buffer of the most recent
// bytes, supporting lookup by distance back from the newest byte.
// It depends on no other package.
package window

import "errors"

// ErrCapacity reports a non-positive window capacity.
var ErrCapacity = errors.New("window: capacity must be positive")

// Window is a ring buffer holding the last Capacity() bytes added.
// The zero value is unusable; construct with New. Not goroutine-safe.
type Window struct {
	buf  []byte
	next int // write position of the next byte
	n    int // number of valid bytes, <= len(buf)
}

// New returns a Window with the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the window capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently held.
func (w *Window) Len() int { return w.n }

// Add appends one byte, evicting the oldest when full.
func (w *Window) Add(b byte) {
	w.buf[w.next] = b
	w.next = (w.next + 1) % len(w.buf)
	if w.n < len(w.buf) {
		w.n++
	}
}

// At returns the byte at the given distance back: dist 1 is the most
// recently added byte. It panics when dist is out of range; callers
// (matcher, decoder) guarantee 1 <= dist <= Len().
func (w *Window) At(dist int) byte {
	return w.buf[(w.next-dist+len(w.buf))%len(w.buf)]
}

// AppendTo appends the held bytes, oldest first, to dst.
func (w *Window) AppendTo(dst []byte) []byte {
	for i := w.n; i >= 1; i-- {
		dst = append(dst, w.At(i))
	}
	return dst
}
