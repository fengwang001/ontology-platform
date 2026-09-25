// Package window implements a fixed-capacity ring buffer of recent bytes.
package window

import "errors"

// ErrBadConfig reports an illegal window capacity.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window keeps at most cap bytes: the newest cap bytes ever appended.
type Window struct {
	buf []byte
	cap int
	n   int // total bytes ever appended (logical cursor)
}

// New creates a window with the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity), cap: capacity}, nil
}

// Cap reports the configured capacity.
func (w *Window) Cap() int { return w.cap }

// Len reports how many bytes are currently available (<= cap).
func (w *Window) Len() int {
	if w.n < w.cap {
		return w.n
	}
	return w.cap
}

// Append stores p, evicting the oldest bytes when capacity is exceeded.
func (w *Window) Append(p []byte) {
	for _, c := range p {
		w.buf[w.n%w.cap] = c
		w.n++
	}
}

// At returns the byte at logical distance d from the newest byte:
// d=1 is the last appended byte. d must be <= Len().
func (w *Window) At(distance int) byte {
	return w.buf[((w.n-distance)%w.cap+w.cap)%w.cap]
}

// Equal reports whether the len(needle) bytes ending at distance from the
// newest byte equal needle. It must not be called with a short history.
func (w *Window) Equal(distance int, needle []byte) bool {
	for i := len(needle) - 1; i >= 0; i-- {
		if w.At(distance+(len(needle)-1-i)) != needle[i] {
			return false
		}
	}
	return true
}

// Copy appends length bytes to dst by copying from distance, correctly
// handling overlapping references (distance < length): each produced byte
// becomes part of the history for the next one.
func (w *Window) Copy(dst []byte, distance, length int) []byte {
	for i := 0; i < length; i++ {
		c := w.At(distance)
		dst = append(dst, c)
		w.buf[w.n%w.cap] = c
		w.n++
	}
	return dst
}

// Tail appends the last min(Len(), n) stored bytes to dst.
func (w *Window) Tail(dst []byte, n int) []byte {
	if n > w.Len() {
		n = w.Len()
	}
	for i := n; i > 0; i-- {
		dst = append(dst, w.At(i))
	}
	return dst
}
