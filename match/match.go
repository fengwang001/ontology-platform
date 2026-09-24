package match

// Package match finds longest matches inside a sliding window using a
// bounded-length hash chain. It depends only on packages window and wire.

import (
	"errors"

	"ontology/window"
	"ontology/wire"
)

var ErrZeroChain = errors.New("match: max chain must be positive")

const hashSlots = 1 << 16

// Matcher indexes every appended byte in stream order.
type Matcher struct {
	w        *window.Window
	maxChain int
	head     [hashSlots]uint64 // newest sequence number for a hash
	prev     []uint64          // ring indexed by (seq-1) mod window cap
	nextSeq  uint64
	probes   uint64 // unexported count of examined candidate positions
	c0, c1   byte   // two bytes preceding the next Index call
	have     int
}

// New creates a matcher over w with candidate-chain limit maxChain.
func New(w *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrZeroChain
	}
	return &Matcher{w: w, maxChain: maxChain, prev: make([]uint64, w.Cap())}, nil
}

// Probes reports the total number of candidate positions examined.
func (m *Matcher) Probes() uint64 { return m.probes }

// Reset clears chain state and the preset-dictionary carry.
func (m *Matcher) Reset() {
	m.head = [hashSlots]uint64{}
	m.nextSeq, m.probes, m.have = 0, 0, 0
}

func hash3(a, b, c byte) uint32 {
	return (uint32(a)<<14 + uint32(b)<<7 + uint32(c)) & (hashSlots - 1)
}

// Index appends p to the window and inserts its 3-byte hashes. Calls must
// follow stream order (preset dictionary first, then committed input).
func (m *Matcher) Index(p []byte) {
	m.w.Append(p)
	for i := 0; i < len(p); i++ {
		seq := m.nextSeq + 1
		m.nextSeq = seq
		if m.have == 2 {
			h := hash3(m.c0, m.c1, p[i])
			m.prev[(seq-1)&uint64(len(m.prev)-1)] = m.head[h]
			m.head[h] = seq
		}
		m.c0, m.c1 = m.c1, p[i]
		if m.have < 2 {
			m.have++
		}
	}
}

// Find returns the longest match (distance, length) of pending's prefix
// verifiable inside the window; length 0 means nothing >= wire.MinMatch.
// length never exceeds maxLen or len(pending).
func (m *Matcher) Find(pending []byte, maxLen int) (dist, length int) {
	if len(pending) < wire.MinMatch || m.w.Len() < wire.MinMatch {
		return 0, 0
	}
	lim := len(pending)
	if lim > maxLen {
		lim = maxLen
	}
	seq := m.head[hash3(pending[0], pending[1], pending[2])]
	for c := 0; c < m.maxChain && seq != 0; c++ {
		m.probes++
		age := m.nextSeq - seq
		if age == 0 || age > uint64(m.w.Len()) {
			break // stale slot after ring reuse
		}
		d := int(age)
		l := 0
		for l < lim && l < d && pending[l] == m.w.At(d-l) {
			l++
		}
		if l >= wire.MinMatch && l > length {
			dist, length = d, l
		}
		if l == lim {
			break
		}
		seq = m.prev[(seq-1)&uint64(len(m.prev)-1)]
	}
	return dist, length
}
