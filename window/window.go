// Package window is a fixed-capacity ring buffer of historical bytes.
package window

import "errors"

var ErrDistTooFar = errors.New("window: distance exceeds bytes available")

// Window stores the most recent Cap bytes under monotonically increasing
// absolute positions. Valid positions are [End-Len, End).
type Window struct {
	buf  []byte
	cap  int
	end  int64
	size int
}

// New builds a window of the given positive capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, errors.New("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity), cap: capacity}, nil
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return w.cap }

// End is the absolute position one past the newest byte.
func (w *Window) End() int64 { return w.end }

// Len is the number of bytes currently retained.
func (w *Window) Len() int { return w.size }

// Add appends one byte, evicting the oldest byte when full.
func (w *Window) Add(b byte) {
	w.buf[w.end%int64(w.cap)] = b
	w.end++
	if w.size < w.cap {
		w.size++
	}
}

// At returns the byte at absolute position p.
func (w *Window) At(p int64) byte { return w.buf[p%int64(w.cap)] }

// Get returns the byte at distance d behind End (d in 1..Len).
func (w *Window) Get(d int64) (byte, error) {
	if d <= 0 || d > int64(w.size) {
		return 0, ErrDistTooFar
	}
	return w.buf[(w.end-d)%int64(w.cap)], nil
}
