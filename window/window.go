// Package window implements a fixed-capacity sliding window over the
// output history, backed by a ring buffer. Bytes are addressable both
// by distance back from the most recent byte and by absolute position.
package window

import "errors"

// ErrBadCapacity rejects a non-positive window capacity.
var ErrBadCapacity = errors.New("window: capacity must be positive")

// Window is a fixed-capacity ring buffer of the most recent bytes.
type Window struct {
	buf   []byte
	start int
	n     int
	total int64
}

// New creates a window with the given capacity in bytes.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently stored (<= Cap).
func (w *Window) Len() int { return w.n }

// Total returns the number of bytes ever written.
func (w *Window) Total() int64 { return w.total }

// Put appends one byte, evicting the oldest byte when full.
func (w *Window) Put(b byte) {
	if w.n < len(w.buf) {
		w.buf[(w.start+w.n)%len(w.buf)] = b
		w.n++
	} else {
		w.buf[w.start] = b
		w.start = (w.start + 1) % len(w.buf)
	}
	w.total++
}

// PutAll appends a slice of bytes.
func (w *Window) PutAll(p []byte) {
	for _, b := range p {
		w.Put(b)
	}
}

// At returns the byte dist positions back from the most recent byte
// (dist == 1 is the newest). ok is false when dist is out of range.
func (w *Window) At(dist int) (byte, bool) {
	if dist < 1 || dist > w.n {
		return 0, false
	}
	return w.buf[(w.start+w.n-dist)%len(w.buf)], true
}

// ByteAt returns the byte at absolute position pos (0-based over all
// bytes ever written). ok is false when pos has been evicted or is in
// the future.
func (w *Window) ByteAt(pos int64) (byte, bool) {
	if pos < w.total-int64(w.n) || pos >= w.total {
		return 0, false
	}
	return w.At(int(w.total - pos))
}
