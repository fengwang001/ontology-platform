// Package match finds longest matches inside a sliding window with a
// depth-limited hash chain.
package match

import (
	"errors"

	"ontology/window"
)

const (
	// MinMatch is the shortest emitted back-reference length.
	MinMatch = 3
	// MaxMatchLen is the longest match one record may describe.
	MaxMatchLen = 512
	hashSize    = 1 << 16
)

var (
	// ErrZeroChain reports a non-positive chain depth.
	ErrZeroChain = errors.New("match: maxChain must be > 0")
	// ErrSmallMatch reports a match cap below MinMatch.
	ErrSmallMatch = errors.New("match: maxMatch must be >= 3")
)

// Matcher is a hash-chain matcher over an owned sliding window.
type Matcher struct {
	win      *window.Window
	head     [hashSize]int
	prev     []int
	total    int
	maxChain int
	maxMatch int
	examined int64
}

// New builds a matcher over a window of winCap bytes.
func New(winCap, maxChain, maxMatch int) (*Matcher, error) {
	w, err := window.New(winCap)
	if err != nil {
		return nil, err
	}
	if maxChain <= 0 {
		return nil, ErrZeroChain
	}
	if maxMatch < MinMatch {
		return nil, ErrSmallMatch
	}
	m := &Matcher{win: w, prev: make([]int, winCap), maxChain: maxChain, maxMatch: maxMatch}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m, nil
}

// Win exposes the underlying window.
func (m *Matcher) Win() *window.Window { return m.win }

// Examined reports the cumulative number of candidate positions inspected.
func (m *Matcher) Examined() int64 { return m.examined }

func hash3(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
}

// insert registers the 3-gram ending at the newest byte position p.
func (m *Matcher) insert(p int, gram []byte) {
	h := hash3(gram)
	m.prev[p%len(m.prev)] = m.head[h]
	m.head[h] = p
}

// Add appends bytes, registering every new 3-gram in the chains.
func (m *Matcher) Add(b []byte) {
	ringCap := len(m.prev)
	for _, c := range b {
		p := m.total
		if m.total >= ringCap {
			m.prev[p%ringCap] = -1 // oldest position is leaving the ring
		}
		m.win.Add(c)
		if m.total >= MinMatch-1 {
			g := []byte{m.win.Byte(3), m.win.Byte(2), m.win.Byte(1)}
			m.insert(p, g)
		}
		m.total++
	}
}

// Preset loads a dictionary (e.g. the previous block's tail) before input.
func (m *Matcher) Preset(dict []byte) {
	ringCap := len(m.prev)
	if len(dict) > ringCap {
		dict = dict[len(dict)-ringCap:]
	}
	m.Add(dict)
}

// Find returns the distance and length of the best match for lookahead[0].
// Length 0 means no match of at least MinMatch. At most maxChain candidates
// are inspected; each inspected candidate increments Examined.
func (m *Matcher) Find(lookahead []byte) (dist, length int) {
	if len(lookahead) < MinMatch {
		return 0, 0
	}
	limit := m.maxMatch
	if limit > len(lookahead) {
		limit = len(lookahead)
	}
	h := hash3(lookahead)
	cand := m.head[h]
	maxDist := m.win.Len()
	for n := 0; n < m.maxChain && cand >= 0; n++ {
		m.examined++
		d := m.total - cand
		if d <= 0 || d > maxDist {
			break
		}
		l := 0
		for l < limit && m.win.Byte(d+l) == lookahead[l] {
			l++
		}
		if l > length {
			length, dist = l, d
			if length == limit {
				break
			}
		}
		cand = m.prev[cand%len(m.prev)]
	}
	return dist, length
}
