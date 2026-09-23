// Package window is a fixed-capacity ring buffer of recent bytes.
package window

// Window stores the last Cap bytes appended.
type Window struct {
	ring  []byte
	cap   int
	total int
}

// New returns an empty window; cap must be positive.
func New(cap int) *Window {
	if cap <= 0 {
		panic("window: capacity must be positive")
	}
	return &Window{cap: cap, ring: make([]byte, cap)}
}

// Cap returns the fixed capacity.
func (w *Window) Cap() int { return w.cap }

// Len returns the total number of bytes ever appended (absolute stream size).
func (w *Window) Len() int { return w.total }

// Reset empties the window.
func (w *Window) Reset() { w.total = 0 }

// Append adds all bytes to the window, discarding the oldest beyond capacity.
func (w *Window) Append(b []byte) {
	for _, c := range b {
		w.ring[w.total%w.cap] = c
		w.total++
	}
}

// At returns the byte at absolute position pos.
// pos must satisfy Len()-Cap() <= pos < Len().
func (w *Window) At(pos int) byte {
	return w.ring[pos%w.cap]
}
