// Package match finds longest matches in a sliding window via hash chains.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest match length the compressor emits.
const MinMatch = 3

const hashBits = 16
const hashSize = 1 << hashBits

// ErrBadConfig is returned for invalid configuration.
var ErrBadConfig = errors.New("match: max chain must be positive")

// Matcher indexes window contents with one hash chain per 3-byte key.
// Chain slots form a ring of capacity positions; a slot carries the
// generation of its owning position so stale overwritten slots stop walks.
type Matcher struct {
	win   *window.Window
	chain int
	head  []int // most recent absolute position for each hash
	slot  []int // previous-position ring, indexed by pos % cap
	gen   []int // generation of the position stored in each slot
	probe int64 // unexported count of examined candidates
}

// New creates a Matcher over an existing window.
func New(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrBadConfig
	}
	c := win.Cap()
	return &Matcher{
		win:   win,
		chain: maxChain,
		head:  make([]int, hashSize),
		slot:  make([]int, c),
		gen:   make([]int, c),
	}, nil
}

func hash3(b0, b1, b2 byte) int {
	return (int(b0)<<10 ^ int(b1)<<5 ^ int(b2)) & (hashSize - 1)
}

// Insert indexes the 3-byte sequence ending at the window's newest byte
// (absolute position pos). Bytes before pos-2 are assumed already indexed.
func (m *Matcher) Insert(pos int) {
	if pos < MinMatch-1 || !m.win.Has(pos-2) {
		return
	}
	h := hash3(m.win.At(pos-2), m.win.At(pos-1), m.win.At(pos))
	idx := pos % m.win.Cap()
	m.slot[idx] = m.head[h]
	m.gen[idx] = pos / m.win.Cap()
	m.head[h] = pos
}

// Find looks for the longest match starting at absolute position pos,
// compared against the lookahead bytes (not yet in the window). It walks
// at most maxChain candidates and returns distance and length (0 if none).
func (m *Matcher) Find(pos int, ahead []byte) (dist, length int) {
	if len(ahead) < MinMatch {
		return 0, 0
	}
	h := hash3(ahead[0], ahead[1], ahead[2])
	cand := m.head[h]
	cap := m.win.Cap()
	for k := 0; k < m.chain; k++ {
		if !m.win.Has(cand - 2) {
			return
		}
		m.probe++
		d := pos - cand
		if d < 1 || d > cap {
			return
		}
		l := 0
		maxL := len(ahead)
		if span := m.win.Total() - cand; span < maxL {
			maxL = span
		}
		for l < maxL && m.win.At(cand+l) == ahead[l] {
			l++
		}
		if l > length {
			dist, length = d, l
		}
		idx := cand % cap
		next := m.slot[idx]
		if m.gen[idx] != cand/cap || next >= cand {
			return
		}
		cand = next
	}
	return
}

// Probes reports the total number of candidate positions examined.
func (m *Matcher) Probes() int64 { return m.probe }
