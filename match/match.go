// Package match finds longest backrefs inside a window.Window using
// hash chains with a bounded candidate walk. Depends on window only.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest length encoded as a backref.
const MinMatch = 4

const hashBits = 16

var (
	ErrMaxDist  = errors.New("match: maxDist must be positive")
	ErrMaxChain = errors.New("match: maxChain must be positive")
)

// Matcher finds matches in a window. Not safe for concurrent use.
type Matcher struct {
	win      *window.Window
	maxDist  int
	maxChain int
	head     []int // hash -> newest absolute position, -1 empty
	prev     []int // pos % Cap -> previous position with same hash
	probed   int   // total candidates examined (unexported counter)
}

// New creates a matcher over w. maxDist caps backref distances,
// maxChain caps candidates examined per Longest call.
func New(w *window.Window, maxDist, maxChain int) (*Matcher, error) {
	if maxDist < 1 {
		return nil, ErrMaxDist
	}
	if maxChain < 1 {
		return nil, ErrMaxChain
	}
	head := make([]int, 1<<hashBits)
	for i := range head {
		head[i] = -1
	}
	prev := make([]int, w.Cap())
	for i := range prev {
		prev[i] = -1
	}
	return &Matcher{win: w, maxDist: maxDist, maxChain: maxChain,
		head: head, prev: prev}, nil
}

// Probed returns the total number of candidates examined so far.
func (m *Matcher) Probed() int { return m.probed }

// Insert adds absolute position pos to the chains. Requires
// pos+MinMatch <= win.Pos() so the hash bytes exist.
func (m *Matcher) Insert(pos int) {
	h := m.hash4(pos)
	m.prev[pos%len(m.prev)] = m.head[h]
	m.head[h] = pos
}

// Longest returns the best match at absolute position pos, capped
// at maxLen bytes. Returns (0, 0) when no match reaches MinMatch.
func (m *Matcher) Longest(pos, maxLen int) (bestDist, bestLen int) {
	if maxLen < MinMatch {
		return 0, 0
	}
	budget := m.maxChain
	if pos >= 1 && budget > 0 { // explicit distance-1 candidate
		m.probed++
		budget--
		if l := m.cmp(pos-1, pos, maxLen); l > bestLen {
			bestDist, bestLen = 1, l
		}
	}
	if bestLen < maxLen && pos+MinMatch <= m.win.Pos() {
		for cand := m.head[m.hash4(pos)]; cand >= 0 && budget > 0; {
			d := pos - cand
			if d > m.maxDist {
				break
			}
			m.probed++
			budget--
			if l := m.cmp(cand, pos, maxLen); l > bestLen {
				bestDist, bestLen = d, l
				if bestLen >= maxLen {
					break
				}
			}
			cand = m.prev[cand%len(m.prev)]
		}
	}
	if bestLen < MinMatch {
		return 0, 0
	}
	return bestDist, bestLen
}

func (m *Matcher) cmp(cand, pos, maxLen int) int {
	l := 0
	for l < maxLen {
		a, ok := m.win.At(cand + l)
		if !ok {
			break
		}
		b, ok := m.win.At(pos + l)
		if !ok || a != b {
			break
		}
		l++
	}
	return l
}

func (m *Matcher) hash4(pos int) int {
	v := uint32(0)
	for i := 0; i < MinMatch; i++ {
		b, _ := m.win.At(pos + i)
		v = v<<8 | uint32(b)
	}
	return int((v * 2654435761) >> (32 - hashBits))
}
