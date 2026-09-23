package window

import "errors"

// ErrBadConfig is returned for illegal capacities.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window is a fixed-capacity ring buffer of the most recent bytes.
// It is not safe for concurrent use.
type Window struct {
	buf  []byte
	size int
	next int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

func (w *Window) Push(b byte) {
	w.buf[w.next] = b
	w.next = (w.next + 1) % len(w.buf)
	if w.size < len(w.buf) {
		w.size++
	}
}

// ByteAt returns the byte dist positions back (dist>=1, dist<=Len).
func (w *Window) ByteAt(dist int) byte {
	idx := w.next - dist
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}

func (w *Window) Len() int { return w.size }
func (w *Window) Cap() int { return len(w.buf) }

// Last returns up to n most recent bytes, oldest first. The slice may be
// freshly allocated and is independent of the ring.
func (w *Window) Last(n int) []byte {
	if n > w.size {
		n = w.size
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = w.ByteAt(n - i)
	}
	return out
}
