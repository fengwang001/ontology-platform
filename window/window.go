// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

// Window keeps at most Cap bytes; older bytes are overwritten.
type Window struct {
	buf []byte
	pos int
	n   int
}

// New creates a window of the given positive capacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity)}
}

func (w *Window) Cap() int { return len(w.buf) }

// Len is the number of bytes currently available (<= Cap).
func (w *Window) Len() int { return w.n }

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.n < len(w.buf) {
		w.n++
	}
}

// At returns the byte at distance d from the newest byte (d >= 1).
func (w *Window) At(d int) byte {
	if d < 1 || d > w.n {
		panic("window: distance out of range")
	}
	idx := w.pos - d
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}

// Reset empties the window without reallocating.
func (w *Window) Reset() {
	w.pos, w.n = 0, 0
}
