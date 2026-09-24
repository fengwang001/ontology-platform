// Package window is a fixed-capacity ring buffer of already-produced bytes.
package window

import "errors"

var ErrZeroCapacity = errors.New("window: capacity must be positive")

// Window stores the last Cap bytes. Positions are addressed by absolute
// distance from the newest byte (1 = most recent byte).
type Window struct {
	buf  []byte
	mask uint64
	len  int
	// next is the absolute byte count ever appended; newest slot is next-1.
	next uint64
}

// New returns a window whose capacity is the smallest power of two >= capHint.
func New(capHint int) (*Window, error) {
	if capHint <= 0 {
		return nil, ErrZeroCapacity
	}
	c := 1
	for c < capHint {
		c <<= 1
	}
	return &Window{buf: make([]byte, c), mask: uint64(c - 1)}, nil
}

// Cap reports the ring capacity.
func (w *Window) Cap() int { return len(w.buf) }

// Len reports how many bytes are currently available (<= Cap).
func (w *Window) Len() int { return w.len }

// Reset empties the window.
func (w *Window) Reset() { w.len = 0; w.next = 0 }

// Append stores bytes, dropping the oldest beyond capacity.
func (w *Window) Append(p []byte) {
	for _, b := range p {
		w.buf[w.next&w.mask] = b
		w.next++
		if w.len < len(w.buf) {
			w.len++
		}
	}
}

// At returns the byte at absolute distance d (d>=1) from the newest byte.
func (w *Window) At(d int) byte {
	return w.buf[(w.next-uint64(d))&w.mask]
}

// Copy appends length bytes produced by a back-reference at distance d to dst,
// copying one byte at a time forward, which correctly handles d < length
// (overlapping, periodic copies). Bytes are also appended to the window.
func (w *Window) Copy(dst []byte, d, length int) []byte {
	for i := 0; i < length; i++ {
		b := w.At(d)
		dst = append(dst, b)
		w.buf[w.next&w.mask] = b
		w.next++
		if w.len < len(w.buf) {
			w.len++
		}
	}
	return dst
}
