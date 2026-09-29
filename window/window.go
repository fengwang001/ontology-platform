// Package window is a fixed-capacity ring buffer of recent bytes.
package window

// Window keeps the most recent cap bytes.
type Window struct {
	buf      []byte
	pos      int // index where the next byte is written
	n        int // number of live bytes
}

// New creates a window with the given positive capacity.
func New(capacity int) *Window {
	return &Window{buf: make([]byte, capacity)}
}

// Cap returns the capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns bytes currently stored.
func (w *Window) Len() int { return w.n }

// Put appends one byte.
func (w *Window) Put(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.n < len(w.buf) {
		w.n++
	}
}

// At returns the byte at distance d (1 = most recent).
func (w *Window) At(d int) byte {
	i := w.pos - d
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}
