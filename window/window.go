// Package window implements a fixed-capacity ring buffer of bytes.
package window

import "errors"

// ErrBadConfig is returned for invalid window configuration.
var ErrBadConfig = errors.New("window: capacity must be positive")

// Window keeps the most recent Cap bytes. Positions are absolute byte
// offsets from the start of the logical stream.
type Window struct {
	buf  []byte
	cap  int
	size int // bytes currently stored (<= cap)
	head int // index of the oldest byte
	tail int // total bytes ever appended
}

// New creates a window of the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity), cap: capacity}, nil
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return w.cap }

// Len returns the number of bytes currently held.
func (w *Window) Len() int { return w.size }

// Total returns the number of bytes appended over the whole stream.
func (w *Window) Total() int { return w.tail }

// Append stores bytes, evicting the oldest when full.
func (w *Window) Append(p []byte) {
	w.tail += len(p)
	if len(p) >= w.cap {
		copy(w.buf, p[len(p)-w.cap:])
		w.size = w.cap
		w.head = 0
		return
	}
	for _, c := range p {
		if w.size == w.cap {
			w.buf[w.head] = c
			w.head = (w.head + 1) % w.cap
		} else {
			idx := (w.head + w.size) % w.cap
			w.buf[idx] = c
			w.size++
		}
	}
}

// At returns the byte at absolute position pos. The caller must ensure the
// position is still present (pos >= Total()-Len()).
func (w *Window) At(pos int) byte {
	off := pos - (w.tail - w.size)
	return w.buf[(w.head+off)%w.cap]
}

// Has reports whether absolute position pos is still stored.
func (w *Window) Has(pos int) bool {
	return pos >= 0 && pos >= w.tail-w.size && pos < w.tail
}

// Copy appends the byte at distance d (1 = most recent) to dst and advances
// the window by that emitted byte; used for overlap-safe decompression.
func (w *Window) EmitCopy(dst []byte, d int) []byte {
	c := w.At(w.tail - d)
	w.Append([]byte{c})
	return append(dst, c)
}
