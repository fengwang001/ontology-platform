// Package window implements a fixed-capacity ring buffer of emitted bytes.
package window

// Window stores at most Cap most recent bytes.
type Window struct {
	buf []byte
	pos int
	n   int
}

// New creates a window; capacity must be positive.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{buf: make([]byte, capacity), pos: -1}
}

// Cap returns the configured capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of stored bytes.
func (w *Window) Len() int { return w.n }

// Add appends one byte, overwriting the oldest when full.
func (w *Window) Add(b byte) {
	w.pos++
	if w.pos == len(w.buf) {
		w.pos = 0
	}
	w.buf[w.pos] = b
	if w.n < len(w.buf) {
		w.n++
	}
}

// AddAll appends every byte (used for preset dictionaries).
func (w *Window) AddAll(p []byte) {
	for _, b := range p {
		w.Add(b)
	}
}

// At returns the byte at 1-based position, 1 being the oldest stored.
func (w *Window) At(pos int) byte {
	if pos <= 0 || pos > w.n {
		panic("window: position out of range")
	}
	idx := pos - 1
	if w.n == len(w.buf) {
		idx = (w.pos + 1 + pos - 1) % len(w.buf)
	}
	return w.buf[idx]
}

// Last returns the byte at distance dist (1 = most recent).
func (w *Window) Last(dist int) byte { return w.At(w.n - dist + 1) }

// Tail copies up to n most recent bytes into dst and returns the count.
func (w *Window) Tail(n int) []byte {
	if n > w.n {
		n = w.n
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = w.Last(n - i)
	}
	return out
}
