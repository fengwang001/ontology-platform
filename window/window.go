// Package window is a fixed-capacity ring buffer of the most recent
// output bytes, addressed by distance (1 = most recent).
package window

import "errors"

var ErrBadCapacity = errors.New("window: capacity must be positive")

type Window struct {
	buf  []byte
	head int // index of oldest byte
	n    int
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

func (w *Window) Cap() int { return len(w.buf) }

func (w *Window) Len() int { return w.n }

func (w *Window) Add(b byte) {
	if w.n < len(w.buf) {
		w.buf[(w.head+w.n)%len(w.buf)] = b
		w.n++
		return
	}
	w.buf[w.head] = b
	w.head = (w.head + 1) % len(w.buf)
}

func (w *Window) AddBytes(p []byte) {
	for _, b := range p {
		w.Add(b)
	}
}

// At returns the byte at distance dist (1 = most recent). Caller must
// guarantee 1 <= dist <= Len().
func (w *Window) At(dist int) byte {
	return w.buf[(w.head+w.n-dist)%len(w.buf)]
}

// Peek copies out k bytes starting at distance dist, in emission order
// (oldest first). Caller must guarantee 0 <= k <= dist <= Len().
func (w *Window) Peek(dist, k int) []byte {
	out := make([]byte, k)
	for i := range out {
		out[i] = w.buf[(w.head+w.n-dist+i)%len(w.buf)]
	}
	return out
}

// Bytes returns the window contents, oldest first.
func (w *Window) Bytes() []byte { return w.Peek(w.n, w.n) }
