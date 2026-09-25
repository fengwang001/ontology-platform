// Package window implements a fixed-capacity circular byte buffer used as the
// LZ77 history window.
package window

import "ontology/wire"

// Window is a ring buffer retaining at most Cap most recent bytes.
// A single Window is not safe for concurrent use.
type Window struct {
	buf   []byte
	start int // index of the oldest retained byte
	size  int // number of retained bytes
	total int // total bytes ever appended
}

// New creates a window with the given capacity. Capacity must be positive.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, wire.ErrBadConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently retained.
func (w *Window) Len() int { return w.size }

// Total returns the number of bytes ever appended.
func (w *Window) Total() int { return w.total }

// Write appends bytes, evicting the oldest bytes beyond capacity.
func (w *Window) Write(p []byte) {
	if len(p) >= len(w.buf) {
		keep := p[len(p)-len(w.buf):]
		copy(w.buf, keep)
		w.start = 0
		w.size = len(w.buf)
		w.total += len(p)
		return
	}
	for _, b := range p {
		w.buf[(w.start+w.size)%len(w.buf)] = b
		if w.size < len(w.buf) {
			w.size++
		} else {
			w.start = (w.start + 1) % len(w.buf)
		}
	}
	w.total += len(p)
}

// At returns the byte at the given 1-based distance from the newest byte:
// At(1) is the most recently appended byte.
func (w *Window) At(distance int) byte {
	if distance <= 0 || distance > w.size {
		panic("window: distance out of range")
	}
	return w.buf[(w.start+w.size-distance)%len(w.buf)]
}

// ByteAt returns the byte at an absolute stream position.
func (w *Window) ByteAt(abs int) byte {
	return w.At(w.total - abs)
}

// Snapshot appends the retained bytes (oldest first) to dst.
func (w *Window) Snapshot(dst []byte) []byte {
	for i := 0; i < w.size; i++ {
		dst = append(dst, w.buf[(w.start+i)%len(w.buf)])
	}
	return dst
}
