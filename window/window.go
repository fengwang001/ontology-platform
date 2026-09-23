// Package window is a fixed-capacity ring buffer of emitted bytes.
package window

import "errors"

// ErrBadConfig is returned for invalid capacities.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window stores the most recent Cap bytes plus an unbounded emitted count.
type Window struct {
	buf     []byte
	cap     int
	emitted int // absolute number of bytes ever written
	size    int // valid bytes currently in the ring
	head    int // index where the next byte is written
}

// New creates a window holding at most cap bytes.
func New(cap int) (*Window, error) {
	if cap <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, cap), cap: cap}, nil
}

// Cap returns the ring capacity.
func (w *Window) Cap() int { return w.cap }

// Emitted returns the total number of bytes ever written.
func (w *Window) Emitted() int { return w.emitted }

// Size returns how many bytes are currently available.
func (w *Window) Size() int { return w.size }

// Put appends one byte, evicting the oldest when full.
func (w *Window) Put(c byte) {
	w.buf[w.head] = c
	w.head++
	if w.head == w.cap {
		w.head = 0
	}
	if w.size < w.cap {
		w.size++
	}
	w.emitted++
}

// Write appends a slice.
func (w *Window) Write(p []byte) {
	for _, c := range p {
		w.Put(c)
	}
}

// At returns the byte at absolute position pos (0-based over all emissions).
// Positions older than the retained capacity are inaccessible.
func (w *Window) At(pos int) byte {
	off := pos - (w.emitted - w.size)
	idx := (w.head - w.size + off) % w.cap
	if idx < 0 {
		idx += w.cap
	}
	return w.buf[idx]
}

// Reset empties the ring and clears the emission counter.
func (w *Window) Reset() {
	w.head, w.size, w.emitted = 0, 0, 0
}
