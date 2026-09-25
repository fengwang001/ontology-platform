// Package window implements a fixed-capacity sliding window over the
// most recently written bytes, addressable by distance or absolute
// position.
package window

import "errors"

// Window is a ring buffer holding the last Cap() written bytes.
type Window struct {
	buf   []byte
	start int   // ring index of the oldest byte
	n     int   // number of valid bytes
	base  int64 // absolute position of the oldest byte
}

// New returns a window with the given capacity; capacity must be > 0.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, errors.New("window: capacity must be > 0")
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the window capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently held.
func (w *Window) Len() int { return w.n }

// Base returns the absolute position of the oldest byte held.
func (w *Window) Base() int64 { return w.base }

// Write appends one byte, evicting the oldest when full.
func (w *Window) Write(b byte) {
	if w.n < len(w.buf) {
		w.buf[(w.start+w.n)%len(w.buf)] = b
		w.n++
		return
	}
	w.buf[w.start] = b
	w.start = (w.start + 1) % len(w.buf)
	w.base++
}

// At returns the byte at the given distance back; 1 is the most recent.
// Requires 1 <= dist <= Len().
func (w *Window) At(dist int) byte {
	return w.buf[(w.start+w.n-dist)%len(w.buf)]
}

// AtAbs returns the byte at an absolute position.
// Requires Base() <= pos < Base()+Len().
func (w *Window) AtAbs(pos int64) byte {
	return w.buf[(w.start+int(pos-w.base))%len(w.buf)]
}
