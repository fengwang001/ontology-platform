// Package window is a fixed-capacity ring buffer of recent input bytes.
// It depends on no other package in this module.
package window

import "errors"

// ErrBadConfig is returned when the capacity is not positive.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window keeps the most recent Cap bytes; older bytes are overwritten.
type Window struct {
	buf   []byte
	start int // absolute position of buf[0]
	size  int // valid bytes currently held
	total int // total bytes ever appended
}

// New creates a window of the given fixed capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Total reports bytes ever appended.
func (w *Window) Total() int { return w.total }

// Start reports the oldest absolute position still retrievable.
func (w *Window) Start() int { return w.start }

// Append stores bytes, dropping the oldest beyond capacity.
func (w *Window) Append(p []byte) {
	w.total += len(p)
	for len(p) > 0 {
		off := (w.start + w.size) % len(w.buf)
		n := copy(w.buf[off:], p)
		p = p[n:]
		if w.size < len(w.buf) {
			w.size += n
			continue
		}
		w.start += n // ring full: oldest bytes are overwritten
	}
}

// At returns the byte dist positions back from the newest (dist >= 1).
func (w *Window) At(dist int) byte {
	return w.Abs(w.total - dist)
}

// Abs returns the byte at absolute stream position pos.
func (w *Window) Abs(pos int) byte {
	return w.buf[(pos-w.start)%len(w.buf)]
}

// Equal reports whether length bytes at absolute positions a and b are equal;
// positions must hold data currently in the window.
func (w *Window) Equal(a, b, length int) bool {
	for i := 0; i < length; i++ {
		if w.Abs(a+i) != w.Abs(b+i) {
			return false
		}
	}
	return true
}

// PrefixLen returns the longest common prefix at absolute positions a and b,
// bounded by limit bytes.
func (w *Window) PrefixLen(a, b, limit int) int {
	i := 0
	for i < limit && w.Abs(a+i) == w.Abs(b+i) {
		i++
	}
	return i
}
