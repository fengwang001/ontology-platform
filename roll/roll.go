// Package roll implements a rolling polynomial hash over a fixed-size byte
// window. Feeding one byte advances the window in O(1) time.
package roll

import "errors"

// ErrZeroWindow is returned when the window length is zero.
var ErrZeroWindow = errors.New("roll: window length must be > 0")

const polyP = uint32(31)

// Hasher maintains a hash of the last w bytes fed to Push.
type Hasher struct {
	w       int
	buf     []byte
	head    int
	size    int
	h       uint32
	pow     uint32
	adv     uint64 // unexported: number of in/out advances performed
	out     byte   // unexported: byte that left on the last advance
	hasOut  bool   // unexported: whether the last Push performed an advance
}

// New creates a Hasher with window length w.
func New(w int) (*Hasher, error) {
	if w <= 0 {
		return nil, ErrZeroWindow
	}
	pow := uint32(1)
	for i := 0; i < w; i++ {
		pow *= polyP
	}
	return &Hasher{w: w, buf: make([]byte, w), pow: pow}, nil
}

// Reset restores the hasher to its empty state.
func (h *Hasher) Reset() {
	h.head, h.size, h.h = 0, 0, 0
	h.adv, h.hasOut = 0, false
}

// Push feeds one byte. The first w calls fill the window; after that every
// call is one O(1) advance (a new byte enters, the oldest leaves).
func (h *Hasher) Push(b byte) {
	h.hasOut = false
	if h.size < h.w {
		h.buf[(h.head+h.size)%h.w] = b
		h.size++
		h.h = uint32(b) + polyP*h.h
		return
	}
	old := h.buf[h.head]
	h.buf[h.head] = b
	h.head = (h.head + 1) % h.w
	h.h = uint32(b) + polyP*h.h - h.pow*uint32(old)
	h.adv++
	h.out, h.hasOut = old, true
}

// Full reports whether the window contains w bytes.
func (h *Hasher) Full() bool { return h.size == h.w }

// Sum returns the current window hash (zero until the window is full).
func (h *Hasher) Sum() uint32 {
	if !h.Full() {
		return 0
	}
	return h.h
}
