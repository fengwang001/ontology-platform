// Package roll implements a rolling window hash with O(1) byte advance.
package roll

import "errors"

// ErrBadWindow is returned when the window length is zero.
var ErrBadWindow = errors.New("roll: window length must be positive")

const base = 1099511628211 // odd prime; keeps every window contribution distinct in mod 2^64

// Hash is a fixed-length sliding-window hash over bytes.
// The zero value is not usable; use New.
type Hash struct {
	w       int
	win     []byte
	pos     int
	filled  bool
	val     uint64
	pow     uint64 // base^(w-1)
	adv     uint64 // unexported counter: number of byte advances
}

// New returns a rolling hash with the given window length.
func New(window int) (*Hash, error) {
	if window <= 0 {
		return nil, ErrBadWindow
	}
	pow := uint64(1)
	for i := 0; i < window-1; i++ {
		pow *= base
	}
	return &Hash{w: window, win: make([]byte, window), pow: pow}, nil
}

// Push feeds one byte into the window.
// While the window is being filled it returns (0,false); afterwards each
// invocation advances the window by exactly one byte and returns the hash.
func (h *Hash) Push(b byte) (uint64, bool) {
	if !h.filled {
		h.win[h.pos] = b
		h.val = h.val*base + uint64(b)
		h.pos++
		if h.pos < h.w {
			return 0, false
		}
		h.filled = true
		h.pos = 0
		return h.val, true
	}
	out := h.win[h.pos]
	h.val = (h.val-uint64(out)*h.pow)*base + uint64(b)
	h.win[h.pos] = b
	h.pos = (h.pos + 1) % h.w
	h.adv++
	return h.val, true
}

// Reset clears the window for a new chunk boundary. It does not touch adv,
// which counts advances over the whole stream.
func (h *Hash) Reset() {
	h.pos = 0
	h.filled = false
	h.val = 0
}

// reportAdvances is package-private; accessed externally only via linkname
// (see cmd/demo), never through the public API.
func reportAdvances(h *Hash) uint64 { return h.adv }
