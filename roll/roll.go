// Package roll implements a fixed-window rolling hash advanced O(1) per byte.
package roll

import "errors"

// ErrWindow is returned when the window length is illegal.
var ErrWindow = errors.New("roll: window length must be > 0")

// Hasher keeps the byte sum inside a sliding window. Push advances the window
// by one byte in constant time; the window never grows past its length.
type Hasher struct {
	window int
	buf    []byte
	pos    int
	filled bool

	sum uint64

	// slides counts "one byte in, one byte out" executions.
	slides uint64
	// pushes counts every byte ever advanced; bytes are never advanced twice.
	pushes uint64
}

// New returns a hasher with the given window length.
func New(window int) (*Hasher, error) {
	if window <= 0 {
		return nil, ErrWindow
	}
	return &Hasher{window: window, buf: make([]byte, window)}, nil
}

// Reset clears all hash state at the start of a new chunk.
func (h *Hasher) Reset() {
	h.pos, h.filled = 0, false
	h.sum, h.slides, h.pushes = 0, 0, 0
}

// Push advances the window by one byte in O(1).
func (h *Hasher) Push(b byte) {
	h.pushes++
	if h.filled {
		h.sum -= uint64(h.buf[h.pos])
		h.slides++
	}
	h.sum += uint64(b)
	h.buf[h.pos] = b
	h.pos++
	if h.pos == h.window {
		h.pos = 0
		h.filled = true
	}
}

// Full reports whether the window contains W bytes.
func (h *Hasher) Full() bool { return h.filled }

// Sum returns the current window hash.
func (h *Hasher) Sum() uint64 { return h.sum }

// Window returns the configured window length.
func (h *Hasher) Window() int { return h.window }

// PushCountForDemo exists only for the demo's progress-count readout.
func (h *Hasher) PushCountForDemo() uint64 { return h.pushes }
