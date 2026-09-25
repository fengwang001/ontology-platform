// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

import "errors"

// ErrConfig reports an illegal (zero) window capacity.
var ErrConfig = errors.New("window: capacity must be > 0")

// Window keeps the newest cap bytes written to it.
type Window struct {
	buf  []byte
	size int // number of valid bytes, capped at cap(buf)
	head int // index of the oldest byte when full
}

// New creates a window holding at most cap bytes.
func New(cap int) (*Window, error) {
	if cap <= 0 {
		return nil, ErrConfig
	}
	return &Window{buf: make([]byte, cap)}, nil
}

// Cap returns the configured capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently stored.
func (w *Window) Len() int { return w.size }

// Reset empties the window.
func (w *Window) Reset() { w.size, w.head = 0, 0 }

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(b byte) {
	if w.size < len(w.buf) {
		w.buf[(w.head+w.size)%len(w.buf)] = b
		w.size++
		return
	}
	w.buf[w.head] = b
	w.head = (w.head + 1) % len(w.buf)
}

// PushN appends many bytes, keeping only the newest cap bytes.
func (w *Window) PushN(p []byte) {
	if len(p) >= len(w.buf) {
		copy(w.buf, p[len(p)-len(w.buf):])
		w.size, w.head = len(w.buf), 0
		return
	}
	for _, b := range p {
		w.Push(b)
	}
}

// At returns the byte at distance d (1-based) from the newest end.
func (w *Window) At(d int) byte {
	return w.buf[(w.head+w.size-d)%len(w.buf)]
}
