// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

import "errors"

// ErrZeroCapacity reports an invalid window size.
var ErrZeroCapacity = errors.New("window: capacity must be > 0")

// Window keeps the last capacity bytes written. Distance 1 is the newest byte.
type Window struct {
	buf  []byte
	size int // number of valid bytes, capped at cap(buf)
	next int // write index (also points at the oldest byte once full)
}

// New returns a window with the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrZeroCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Add appends one byte, evicting the oldest byte when full.
func (w *Window) Add(c byte) {
	w.buf[w.next] = c
	w.next++
	if w.next == len(w.buf) {
		w.next = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// Len reports how many bytes are currently available.
func (w *Window) Len() int { return w.size }

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Byte returns the byte at the given distance: 1 = newest.
func (w *Window) Byte(dist int) byte {
	idx := w.next - dist
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}

// Reset empties the window without releasing its backing array.
func (w *Window) Reset() {
	w.size = 0
	w.next = 0
}
