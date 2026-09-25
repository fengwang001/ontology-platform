// Package window implements a fixed-capacity sliding window over a byte
// stream, addressed by absolute stream positions.
package window

import "errors"

// ErrZeroCapacity rejects a non-positive window capacity.
var ErrZeroCapacity = errors.New("window: capacity must be positive")

// Window is a ring buffer holding the last Cap() bytes ever written.
type Window struct {
	buf   []byte
	total int
}

// New creates a window with the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrZeroCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the window capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Total returns the number of bytes ever written.
func (w *Window) Total() int { return w.total }

// WriteByte appends one byte, evicting the oldest byte when full.
func (w *Window) Put(b byte) {
	w.buf[w.total%len(w.buf)] = b
	w.total++
}

// Write appends p to the window.
func (w *Window) Write(p []byte) {
	for _, b := range p {
		w.Put(b)
	}
}

// At returns the byte at absolute stream position pos; pos must lie in
// [Total()-Cap(), Total()).
func (w *Window) At(pos int) byte { return w.buf[pos%len(w.buf)] }

// Back returns the byte at distance dist behind the write position.
// dist must satisfy 1 <= dist <= min(Cap(), Total()).
func (w *Window) Back(dist int) byte { return w.buf[(w.total-dist)%len(w.buf)] }
