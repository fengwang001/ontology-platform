// Package window implements a fixed-capacity ring buffer of recent bytes.
package window

// Window stores at most Cap bytes of the most recent input.
type Window struct {
	cap int
}

// New builds a Window; capacity must be positive.
func New(capacity int) *Window {
	return &Window{cap: capacity}
}

// Cap reports the fixed capacity.
func (w *Window) Cap() int { return w.cap }

// Len reports how many bytes are currently stored.
func (w *Window) Len() int { return 0 }

// At returns the byte at distance d (1 == most recent).
func (w *Window) At(distance int) byte { return 0 }

// Push appends one byte, evicting the oldest byte when full.
func (w *Window) Push(b byte) {}

// Tail copies up to capacity bytes of the most recent history into dst.
func (w *Window) Tail(dst []byte) []byte { return dst }

// Seed fills an empty window with a preset dictionary prefix.
func (w *Window) Seed(data []byte) {}
