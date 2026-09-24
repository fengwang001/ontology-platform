// Package window is a fixed-capacity ring buffer of recent bytes.
package window

import "errors"

// ErrBadConfig is returned for an invalid capacity.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window keeps the most recent capacity bytes.
type Window struct {
	buf  []byte
	size int
	next int
}

// New returns an empty window.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports how many bytes are currently stored.
func (w *Window) Len() int { return w.size }

// Add appends bytes, evicting oldest bytes past capacity.
func (w *Window) Add(p []byte) {
	for _, b := range p {
		w.buf[w.next] = b
		w.next++
		if w.next == len(w.buf) {
			w.next = 0
		}
		if w.size < len(w.buf) {
			w.size++
		}
	}
}

// At returns the byte at distance d (1 == most recent).
func (w *Window) At(d int) byte {
	idx := w.next - d
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}
