// Package match implements a hash-chain longest-match finder over a sliding
// window. Candidate chains are capped, bounding work per input position.
package match

import (
	"errors"

	"ontology/window"
)

// ErrZeroChainLimit is returned when the chain depth limit is not positive.
var ErrZeroChainLimit = errors.New("match: chain limit must be > 0")

const hashBits = 16

// Matcher finds greedy longest matches using 3-byte hash chains. A Matcher is
// not safe for concurrent use.
type Matcher struct {
	win    *window.Window
	limit  int
	head   []int32 // newest position (virtual coord) per hash, -1
	prev   []int32 // previous-in-chain, keyed by pos mod capacity
	pos    int32   // virtual coord of the newest pushed byte + 1
	probes int64   // total candidate positions examined
}

// New constructs a Matcher with the given window capacity and chain depth.
func New(capacity, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrZeroChainLimit
	}
	win, err := window.New(capacity)
	if err != nil {
		return nil, err
	}
	return &Matcher{
		win:   win,
		limit: chainLimit,
		head: func() []int32 {
			s := make([]int32, 1<<hashBits)
			for i := range s {
				s[i] = -1
			}
			return s
		}(),
		prev: make([]int32, capacity),
	}, nil
}

// Window exposes the backing window.
func (m *Matcher) Window() *window.Window { return m.win }

// Pos returns the number of bytes pushed so far.
func (m *Matcher) Pos() int { return int(m.pos) }

// Capacity returns the window capacity.
func (m *Matcher) Capacity() int { return m.win.Capacity() }

// Probes returns the number of candidate positions examined.
func (m *Matcher) Probes() int64 { return m.probes }

// ResetProbes zeroes the counter and returns its previous value.
func (m *Matcher) ResetProbes() int64 {
	p := m.probes
	m.probes = 0
	return p
}

func hash3(b0, b1, b2 byte) uint32 {
	return (uint32(b0)*2654435761 + uint32(b1)*97 + uint32(b2)) & (1<<hashBits - 1)
}

// PushByte feeds a byte into the window and links the 3-byte chain entry for
// the triple that ends at this byte (i.e. starts at pos-2).
func (m *Matcher) PushByte(b byte) {
	m.win.Push(b)
	p := m.pos // coordinate of this byte
	if p >= 2 {
		h := hash3(m.win.At(3), m.win.At(2), b)
		slot := uint32(p-2) % uint32(cap(m.prev))
		m.prev[slot] = m.head[h]
		m.head[h] = int32(p - 2)
	}
	m.pos++
}

// Find returns the best (distance, length) for the bytes in next[0:], where
// length is capped at maxLen and next may include not-yet-pushed bytes. It
// honors overlapping-copy semantics: candidate byte at offset l is window
// byte at distance cd-(l mod cd). length < 3 means no usable match.
func (m *Matcher) Find(next []byte, maxLen int) (dist, length int) {
	if len(next) < 3 || maxLen < 3 {
		return 0, 0
	}
	cand := m.head[hash3(next[0], next[1], next[2])]
	for depth := 0; depth < m.limit && cand >= 0; depth++ {
		m.probes++
		cd := int(m.pos - cand)
		if cd < 1 || cd > m.win.Capacity() {
			break
		}
		l := 0
		for l < maxLen && l < len(next) {
			if m.win.At(cd-l%cd) != next[l] {
				break
			}
			l++
		}
		if l > length {
			dist, length = cd, l
			if l >= maxLen || l >= len(next) {
				break
			}
		}
		slot := uint32(cand) % uint32(cap(m.prev))
		cand = m.prev[slot]
	}
	return dist, length
}
