// Package window implements the fixed-capacity sliding history buffer.
package window

import "errors"

// ErrBadConfig is returned for invalid construction parameters.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window is a fixed-capacity ring buffer of the most recent bytes.
type Window struct {
	buf []byte
	// start is the ring index of the oldest byte; size is the live count.
	start int
	size  int
}

// New creates a window of the given positive capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports how many live bytes are held (<= Cap).
func (w *Window) Len() int { return w.size }

// Reset empties the window.
func (w *Window) Reset() { w.start, w.size = 0, 0 }

// Push appends bytes, evicting the oldest when full.
func (w *Window) Push(p []byte) {
	if len(p) >= len(w.buf) {
		keep := p[len(p)-len(w.buf):]
		copy(w.buf, keep)
		w.start = 0
		w.size = len(w.buf)
		return
	}
	for _, b := range p {
		w.buf[(w.start+w.size)%len(w.buf)] = b
		if w.size < len(w.buf) {
			w.size++
		} else {
			w.start = (w.start + 1) % len(w.buf)
		}
	}
}

// At returns the byte at distance d (1 = most recent). It panics if d is not
// in 1..Len; callers must validate references before use.
func (w *Window) At(d int) byte {
	if d < 1 || d > w.size {
		panic("window: distance out of range")
	}
	return w.buf[(w.start+w.size-d+len(w.buf)*2)%len(w.buf)]
}

// Bytes appends the live history (oldest first) to dst.
func (w *Window) Bytes(dst []byte) []byte {
	for d := w.size; d >= 1; d-- {
		dst = append(dst, w.At(d))
	}
	return dst
}

// Seed replaces the window content with the last min(len(p), cap) bytes of p,
// used as a preset dictionary without changing their logical distances.
func (w *Window) Seed(p []byte) {
	w.Reset()
	if len(p) > len(w.buf) {
		p = p[len(p)-len(w.buf):]
	}
	w.Push(p)
}
