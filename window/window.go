// Package window is a fixed-capacity ring buffer of recent input bytes.
package window

import "errors"

var ErrZeroCapacity = errors.New("window: capacity must be > 0")

type Window struct {
	buf  []byte
	size int
	pos  int // index one past the newest byte
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrZeroCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

func (w *Window) Cap() int { return len(w.buf) }

func (w *Window) Len() int { return w.size }

// Push appends p, keeping only the most recent Cap bytes.
func (w *Window) Push(p []byte) {
	if len(p) >= len(w.buf) {
		copy(w.buf, p[len(p)-len(w.buf):])
		w.size = len(w.buf)
		w.pos = 0
		return
	}
	for _, c := range p {
		w.buf[w.pos] = c
		w.pos++
		if w.pos == len(w.buf) {
			w.pos = 0
		}
	}
	if w.size < len(w.buf) {
		w.size += len(p)
		if w.size > len(w.buf) {
			w.size = len(w.buf)
		}
	}
}

// At returns the byte at distance d from the newest byte (d=1 is newest).
func (w *Window) At(d int) byte {
	i := w.pos - d
	if i < 0 {
		i += len(w.buf)
	}
	return w.buf[i]
}
