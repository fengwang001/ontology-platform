// Package window implements a fixed-capacity ring buffer of the most
// recent bytes. It depends on no other package of this module.
package window

import "errors"

// ErrBadCapacity is returned when the configured capacity is not positive.
var ErrBadCapacity = errors.New("window: capacity must be > 0")

// Window is a power-of-two sized ring retaining the latest Cap() bytes.
type Window struct {
	buf  []byte
	mask uint64
	cap  int
	n    int // total bytes ever written (absolute cursor)
}

// New creates a window retaining at most capacity recent bytes.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	size := 1
	for size < capacity {
		size <<= 1
	}
	return &Window{buf: make([]byte, size), mask: uint64(size - 1), cap: capacity}, nil
}

// Cap is the configured retention capacity.
func (w *Window) Cap() int { return w.cap }

// Len is the number of addressable bytes (<= Cap()).
func (w *Window) Len() int {
	if w.n < w.cap {
		return w.n
	}
	return w.cap
}

// Total is the absolute number of bytes written so far.
func (w *Window) Total() int { return w.n }

// Add appends one byte.
func (w *Window) Add(b byte) {
	w.buf[uint64(w.n)&w.mask] = b
	w.n++
}

// AddAll appends every byte of p.
func (w *Window) AddAll(p []byte) {
	for _, b := range p {
		w.buf[uint64(w.n)&w.mask] = b
		w.n++
	}
}

// AtAbs returns the byte stored at absolute index i (0-based from the
// first byte ever written). The caller must ensure i is still retained.
func (w *Window) AtAbs(i int) byte {
	return w.buf[uint64(i)&w.mask]
}

// At returns the byte at distance (1 == most recently written byte).
func (w *Window) At(distance int) byte {
	return w.buf[uint64(w.n-distance)&w.mask]
}
