// Package window implements a fixed-capacity ring buffer of recent bytes.
package window

// Window is a fixed-capacity ring buffer. The zero value is not usable;
// call New. A single Window is not safe for concurrent use.
type Window struct {
	buf  []byte
	next int // index where the next Push writes
	size int // live bytes, capped at cap(buf)
}

// New creates a Window with the given positive capacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

// Len reports how many bytes are currently stored (<= Cap).
func (w *Window) Len() int { return w.size }

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(b byte) {
	w.buf[w.next] = b
	w.next++
	if w.next == len(w.buf) {
		w.next = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// AtBack returns the byte that is (1+back) positions before the newest one:
// AtBack(0) is the newest byte, AtBack(d-1) is the byte at distance d.
// back must be smaller than Len.
func (w *Window) AtBack(back int) byte {
	i := w.next - 1 - back
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}

// Reset empties the window.
func (w *Window) Reset() {
	w.next, w.size = 0, 0
}
