// Package window is a fixed-capacity ring buffer of the most recent bytes.
package window

import "errors"

var ErrConfig = errors.New("window: capacity must be positive")

type Window struct {
	buf   []byte
	size  int // number of valid bytes, capped at cap(buf)
	next  int // index where the next byte is written
	total int // absolute count of bytes ever written
}

func New(capacity int) *Window {
	if capacity <= 0 {
		panic(ErrConfig)
	}
	return &Window{buf: make([]byte, capacity)}
}

func (w *Window) Capacity() int { return len(w.buf) }

func (w *Window) Len() int { return w.size }

// Total is the absolute number of bytes ever written.
func (w *Window) Total() int { return w.total }

// At returns the byte at distance d (1-based) from the newest byte.
func (w *Window) At(d int) byte {
	idx := w.next - d
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}

func (w *Window) Put(b byte) {
	w.buf[w.next] = b
	w.next++
	if w.next == len(w.buf) {
		w.next = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
	w.total++
}

func (w *Window) Write(p []byte) {
	for _, b := range p {
		w.Put(b)
	}
}
