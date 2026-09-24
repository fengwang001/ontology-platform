// Package window implements a fixed-capacity circular byte buffer.
package window

import "errors"

// ErrConfig is returned when the capacity is not positive.
var ErrConfig = errors.New("window: capacity must be positive")

// Window is a ring buffer holding the most recent Cap bytes. All byte
// positions are addressed by an absolute monotonically increasing index.
type Window struct {
	buf   []byte
	next  int64 // absolute index of the next byte to be written
	count int   // valid bytes present (<= cap)
}

// New creates a window of the given capacity.
func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrConfig
	}
	return &Window{buf: make([]byte, capacity)}, nil
}

// Cap reports the window capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports how many bytes are currently held.
func (w *Window) Len() int { return w.count }

// Next reports the absolute index of the next byte to write.
func (w *Window) Next() int64 { return w.next }

// Put appends one byte.
func (w *Window) Put(b byte) {
	w.buf[w.next%int64(len(w.buf))] = b
	w.next++
	if w.count < len(w.buf) {
		w.count++
	}
}

// At returns the byte at absolute position pos. The caller guarantees that
// pos is a currently held position (0 <= Next-1-pos < Cap).
func (w *Window) At(pos int64) byte {
	return w.buf[pos%int64(len(w.buf))]
}

// ByteAt returns the byte at distance d from the newest byte: d==0 is the most
// recently written byte, d==1 the one before it. The caller guarantees
// 0 <= d < Len.
func (w *Window) ByteAt(d int) byte {
	return w.At(w.next - 1 - int64(d))
}

// Has reports whether absolute position pos is still present in the window.
func (w *Window) Has(pos int64) bool {
	return pos >= 0 && w.next-1-pos < int64(w.count)
}

// Copy appends src as a preset dictionary without it counting as produced
// output on the compressor side; only the last Cap bytes are retained.
func (w *Window) Preset(src []byte) {
	if len(src) > len(w.buf) {
		src = src[len(src)-len(w.buf):]
	}
	for _, b := range src {
		w.Put(b)
	}
}
