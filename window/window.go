// Package window is a fixed-capacity byte ring used as the LZ77 history.
package window

import "errors"

// Window stores the most recent Capacity bytes. The zero value is not usable;
// construct it with New.
type Window struct {
	buf  []byte
	mask int
	cap  int
	head int // index where the next byte is written
	size int
}

// ErrConfig reports an invalid window capacity.
var ErrConfig = errors.New("window: capacity must be > 0")

// New creates a window. Capacity is rounded up to a power of two so that
// distance lookups are a single masked index.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrConfig
	}
	c := 1
	for c < capacity {
		c <<= 1
	}
	return &Window{buf: make([]byte, c), mask: c - 1, cap: capacity}, nil
}

// Capacity reports the logical (requested) history length. Bytes older than it
// are unreachable even though the underlying ring may be a little larger.
func (w *Window) Capacity() int {
	if w == nil {
		return 0
	}
	return w.cap
}

// Len reports how many bytes are currently reachable.
func (w *Window) Len() int { return w.size }

// Put appends one byte, evicting the oldest byte when full.
func (w *Window) Put(b byte) {
	w.buf[w.head] = b
	w.head = (w.head + 1) & w.mask
	if w.size < len(w.buf) {
		w.size++
	}
}

// Write appends a slice byte by byte (used for preset dictionaries).
func (w *Window) Write(p []byte) {
	for _, b := range p {
		w.Put(b)
	}
}

// At returns the byte dist positions back: dist 1 is the most recently Put
// byte. The caller must ensure 1 <= dist <= Len.
func (w *Window) At(dist int) byte {
	return w.buf[(w.head-dist)&w.mask]
}
