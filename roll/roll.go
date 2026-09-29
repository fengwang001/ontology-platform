package roll

import "errors"

var ErrInvalidWindow = errors.New("roll: window length must be positive")

type Hasher struct {
	advances int
	window   []byte
	size     int
	filled   int
	next     int
	pow      uint32
	sum      uint32
}

func New(window int) (*Hasher, error) {
	if window <= 0 {
		return nil, ErrInvalidWindow
	}
	pow := uint32(1)
	for i := 0; i < window; i++ {
		pow *= 257
	}
	return &Hasher{window: make([]byte, window), size: window, pow: pow}, nil
}

func (h *Hasher) Reset() {
	var zero Hasher
	pow := h.pow
	window := h.window
	*h = zero
	h.pow = pow
	h.window = window
}

func (h *Hasher) Push(b byte) uint32 {
	h.advances++
	if h.filled < h.size {
		h.window[h.next] = b
		h.next = (h.next + 1) % h.size
		h.filled++
		h.sum = h.sum*257 + uint32(b)
		return h.sum
	}
	out := h.window[h.next]
	h.window[h.next] = b
	h.next = (h.next + 1) % h.size
	h.sum = h.sum*257 + uint32(b) - uint32(out)*h.pow
	return h.sum
}

func (h *Hasher) Full() bool {
	return h.filled == h.size
}

func (h *Hasher) Sum() uint32 {
	return h.sum
}
