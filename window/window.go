// Package window implements a fixed-capacity ring buffer holding the most
// recent bytes, used to resolve LZ77 back-references by distance.
package window

import "errors"

// ErrZeroCapacity is returned when a Window is constructed with capacity 0.
var ErrZeroCapacity = errors.New("window: capacity must be > 0")

// Window is a fixed-capacity circular byte buffer. It is not safe for
// concurrent use.
type Window struct {
	buf  []byte
	size int // valid bytes, capped at cap(buf)
	next int // write cursor into buf
}

// New returns a Window with the given positive capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrZeroCapacity
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Capacity returns the ring capacity.
func (w *Window) Capacity() int { return len(w.buf) }

// Len returns the number of currently stored bytes.
func (w *Window) Len() int { return w.size }

// Push appends b to the window, evicting oldest bytes past capacity.
func (w *Window) Push(b byte) {
	w.buf[w.next] = b
	w.next++
	if w.next == len(w.buf) {
		w.next = 0
	}
	if w.size < len(w.buf) {
		w.size++
	}
}

// PushSlice appends every byte of p.
func (w *Window) PushSlice(p []byte) {
	for _, c := range p {
		w.Push(c)
	}
}

// At returns the byte at distance d from the newest end (d=1 is newest).
// It panics if d < 1 or d > Len().
func (w *Window) At(d int) byte {
	if d < 1 || d > w.size {
		panic("window: distance out of range")
	}
	idx := w.next - d
	if idx < 0 {
		idx += len(w.buf)
	}
	return w.buf[idx]
}

// Last appends up to n most recent bytes (oldest-first) to dst and returns
// the extended slice. It is used to seed a preset dictionary.
func (w *Window) Last(dst []byte, n int) []byte {
	if n > w.size {
		n = w.size
	}
	start := w.next - n
	if start < 0 {
		start += len(w.buf)
	}
	if start+n <= len(w.buf) {
		return append(dst, w.buf[start:start+n]...)
	}
	dst = append(dst, w.buf[start:]...)
	return append(dst, w.buf[:n-(len(w.buf)-start)]...)
}
