// Package roll computes a fixed-window rolling hash.
package roll

const mod = 1 << 16

// Hasher is a rolling hash over the most recent Window bytes.
type Hasher struct {
	window int
	pow    uint16
	value  uint16
	buf    []byte
	pos    int
	filled bool
	adv    uint64
}

// New returns a hasher using window bytes.
func New(window int) (*Hasher, error) {
	if window <= 0 {
		return nil, ErrInvalidWindow
	}
	pow := uint16(1)
	for i := 0; i < window; i++ {
		pow *= 2
	}
	return &Hasher{window: window, pow: pow, buf: make([]byte, window)}, nil
}

// Add inserts a byte while the window is being filled.
func (h *Hasher) Add(b byte) error {
	if h.filled {
		return ErrFilled
	}
	h.buf[h.pos] = b
	h.pos++
	h.value = uint16((uint32(h.value)*2 + uint32(b)) % mod)
	if h.pos == h.window {
		h.pos = 0
		h.filled = true
	}
	return nil
}

// Advance removes out and inserts in in constant time.
func (h *Hasher) Advance(out, in byte) error {
	if !h.filled {
		return ErrNotFilled
	}
	h.value = uint16((uint32(h.value)*2 + uint32(in) -
		uint32(h.pow)*uint32(out)) % mod)
	h.adv++
	return nil
}

// Value reports the current hash.
func (h *Hasher) Value() uint16 { return h.value }

// Filled reports whether the initial window is full.
func (h *Hasher) Filled() bool { return h.filled }

// Reset clears the hasher without changing its window.
func (h *Hasher) Reset() {
	h.value, h.pos, h.filled, h.adv = 0, 0, false, 0
	for i := range h.buf {
		h.buf[i] = 0
	}
}

// ErrInvalidWindow means the window length is zero or negative.
var ErrInvalidWindow = sentinel("roll: invalid window")

var (
	// ErrFilled means Add was called after the window filled.
	ErrFilled = sentinel("roll: window already filled")
	// ErrNotFilled means Advance was called before the window filled.
	ErrNotFilled = sentinel("roll: window not filled")
)

type sentinel string

func (e sentinel) Error() string { return string(e) }
