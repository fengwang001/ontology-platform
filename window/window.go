// Package window is a fixed-capacity ring buffer of recently seen bytes.
package window

// Window stores the last Cap bytes ever pushed. The zero value is not usable;
// use New.
type Window struct {
	cap  int
	buf  []byte
	size int
	head int // index where the next byte is written
}

// New creates a window with the given positive capacity.
func New(capacity int) *Window {
	if capacity <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{cap: capacity, buf: make([]byte, capacity)}
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return w.cap }

// Size returns how many bytes are currently available (<= Cap).
func (w *Window) Size() int { return w.size }

// Byte returns the byte at distance positions behind the most recently pushed
// byte. distance is 1-based and must satisfy distance <= Size.
func (w *Window) Byte(distance int) byte {
	idx := w.head - distance
	if idx < 0 {
		idx += w.cap
	}
	return w.buf[idx]
}

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(b byte) {
	w.buf[w.head] = b
	w.head++
	if w.head == w.cap {
		w.head = 0
	}
	if w.size < w.cap {
		w.size++
	}
}

// PushBytes appends a slice of bytes.
func (w *Window) PushBytes(p []byte) {
	if len(p) >= w.cap {
		copy(w.buf, p[len(p)-w.cap:])
		w.size = w.cap
		w.head = 0
		return
	}
	for _, b := range p {
		w.Push(b)
	}
}

// Last returns a copy of the min(n, Size) most recently pushed bytes.
func (w *Window) Last(n int) []byte {
	if n > w.size {
		n = w.size
	}
	out := make([]byte, n)
	for i := n; i > 0; i-- {
		out[i-1] = w.Byte(n - i + 1)
	}
	return out
}
