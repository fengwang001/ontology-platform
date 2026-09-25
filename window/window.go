// Package window implements a fixed-capacity sliding history window
// (ring buffer) with access by distance from the newest byte.
package window

import "errors"

// ErrBadCapacity rejects a zero or negative window capacity.
var ErrBadCapacity = errors.New("window: capacity must be > 0")

// Window is a ring buffer holding the most recent Cap() appended bytes.
type Window struct {
	buf   []byte
	start int
	n     int
	total int64
}

// New creates a window with the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrBadCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap returns the capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len returns the number of bytes currently stored.
func (w *Window) Len() int { return w.n }

// Total returns the number of bytes ever appended.
func (w *Window) Total() int64 { return w.total }

// Append adds one byte, evicting the oldest when full.
func (w *Window) Append(b byte) {
	if w.n < len(w.buf) {
		w.buf[(w.start+w.n)%len(w.buf)] = b
		w.n++
	} else {
		w.buf[w.start] = b
		w.start = (w.start + 1) % len(w.buf)
	}
	w.total++
}

// AppendBytes adds a slice, keeping only the last Cap() bytes.
func (w *Window) AppendBytes(p []byte) {
	if len(p) >= len(w.buf) {
		w.start = 0
		w.n = copy(w.buf, p[len(p)-len(w.buf):])
		w.total += int64(len(p))
		return
	}
	for _, b := range p {
		w.Append(b)
	}
}

// At returns the byte at distance dist behind the newest byte
// (dist 1 = newest). ok is false when dist is out of range.
func (w *Window) At(dist int64) (b byte, ok bool) {
	if dist < 1 || dist > int64(w.n) {
		return 0, false
	}
	idx := (w.start + w.n - int(dist)) % len(w.buf)
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx], true
}
