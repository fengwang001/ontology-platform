// Package window is a fixed-capacity circular byte history.
package window

// Window stores at most Cap most recent bytes in a ring buffer.
// Distance 1 is the most recently written byte.
type Window struct {
	buf  []byte
	pos  int // index where the next byte is written
	size int // number of valid bytes (<= cap)
}

// New returns an empty window; cap must be positive.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
}
	return &Window{buf: make([]byte, capacity)}
}

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports the number of valid history bytes.
func (w *Window) Len() int { return w.size }

// Put appends one byte, overwriting the oldest when full.
func (w *Window) Put(b byte) {
	w.buf[w.pos] = b
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// ByteAt returns the byte at distance dist (1 = newest).
// ok is false when dist is 0 or larger than Len.
func (w *Window) ByteAt(dist int) (byte, bool) {
	if dist <= 0 || dist > w.size {
		return 0, false
}
	i := w.pos - dist
	if i < 0 {
		i += len(w.buf)
}
	return w.buf[i], true
}

// Prefill loads already-known bytes (a preset dictionary), oldest first.
// Only the final Cap bytes are retained.
func (w *Window) Prefill(data []byte) {
	start := 0
	if len(data) > len(w.buf) {
		start = len(data) - len(w.buf)
	}
	for _, b := range data[start:] {
		w.Put(b)
	}
}
