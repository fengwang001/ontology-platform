// Package window is a fixed-capacity ring buffer of raw bytes addressed by
// absolute position (the number of bytes seen since construction).
package window

import "errors"

var ErrConfig = errors.New("window: capacity must be positive")

type Window struct {
	cap  int
	base int // absolute position of ring[0]
	len  int // number of valid bytes (<= cap)
	ring []byte
}

func New(capacity int) (*Window, error) {
	if capacity <= 0 {
		return nil, ErrConfig
	}
	return &Window{cap: capacity, ring: make([]byte, capacity)}, nil
}

func (w *Window) Cap() int { return w.cap }

// Base is the absolute position of the oldest byte still addressable.
func (w *Window) Base() int { return w.base }

// Len is the number of bytes currently held.
func (w *Window) Len() int { return w.len }

// End is the absolute position immediately after the last held byte.
func (w *Window) End() int { return w.base + w.len }

func (w *Window) Reset() { w.base, w.len = 0, 0 }

// Append copies p into the ring, evicting the oldest bytes as needed.
func (w *Window) Append(p []byte) {
	if len(p) >= w.cap {
		copy(w.ring, p[len(p)-w.cap:])
		w.base += len(p) - (w.cap - w.len)
		w.len = w.cap
		return
	}
	start := (w.base + w.len) % w.cap
	n := copy(w.ring[start:], p)
	if n < len(p) {
		copy(w.ring, p[n:])
	}
	if w.len+len(p) <= w.cap {
		w.len += len(p)
		return
	}
	w.base += w.len + len(p) - w.cap
	w.len = w.cap
}

// At returns the byte at absolute position p.
func (w *Window) At(p int) byte { return w.ring[(p-w.base)%w.cap] }

// Has reports whether absolute position p is still in the ring.
func (w *Window) Has(p int) bool { return p >= w.base && p < w.base+w.len }

// Slice returns up to n bytes starting at absolute position p. It never
// returns bytes beyond End(); the result may alias the ring and must be
// consumed before the next Append.
func (w *Window) Slice(p, n int) []byte {
	if p < w.base {
		return nil
	}
	if n > w.base+w.len-p {
		n = w.base + w.len - p
	}
	if n <= 0 {
		return nil
	}
	start := (p - w.base) % w.cap
	if start+n <= w.cap {
		return w.ring[start : start+n]
	}
	return append(w.ring[start:start:w.cap], w.ring[:n-(w.cap-start)]...)
}
