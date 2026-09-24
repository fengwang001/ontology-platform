// Package match finds longest LZ77 matches with a bounded hash chain.
package match

import (
	"errors"

	"ontology/window"
)

var (
	errZeroChain = errors.New("match: chain length must be positive")
)

const MinMatch = 3

// Matcher indexes the window with one hash chain per 3-byte prefix.
type Matcher struct {
	win      *window.Window
	chainLen int
	head     []int64 // hash -> most recent position, -1 empty
	prev     []int64 // ring: pos%cap -> previous position in chain
	inserted int64   // all positions < inserted have been indexed
	candidates int64 // unexported: total candidate positions examined
}

// New builds a matcher over a window of capacity cap and chain bound chainLen.
func New(cap, chainLen int) (*Matcher, error) {
	if chainLen <= 0 {
		return nil, errZeroChain
	}
	win, err := window.New(cap)
	if err != nil {
		return nil, err
	}
	return &Matcher{
		win:      win,
		chainLen: chainLen,
		head:     make([]int64, cap*4),
		prev:     make([]int64, cap),
		inserted: 0,
	}, nil
}

// Win exposes the underlying window.
func (m *Matcher) Win() *window.Window { return m.win }

// Candidates returns how many candidate positions have ever been examined.
func (m *Matcher) Candidates() int64 { return m.candidates }

func (m *Matcher) hashAt(p int64) int {
	h := int(m.win.At(p))<<16 ^ int(m.win.At(p+1))<<8 ^ int(m.win.At(p+2))
	return h & (len(m.head) - 1)
}

// IndexPosition indexes one finalized position; p..p+2 must all be in the window.
func (m *Matcher) IndexPosition(p int64) {
	if p < m.inserted {
		return
	}
	h := m.hashAt(p)
	m.prev[p%int64(m.win.Cap())] = m.head[h]
	m.head[h] = p
	m.inserted = p + 1
}

// Find returns the best (distance,length) match for target starting at absolute
// position pos. follow holds the target's bytes (pos, pos+1, ...), so matches may
// extend into not-yet-finalized pending bytes. Returns length 0 when no match.
func (m *Matcher) Find(pos int64, follow []byte) (dist int64, length int) {
	if len(follow) < MinMatch {
		return 0, 0
	}
	h := (int(follow[0])<<16 ^ int(follow[1])<<8 ^ int(follow[2])) & (len(m.head) - 1)
	lo := pos - int64(m.win.Cap()) + 1
	if lo < 0 {
		lo = 0
	}
	cand := m.head[h]
	for i := 0; i < m.chainLen && cand >= 0; i++ {
		m.candidates++
		if cand >= pos {
			break
		}
		if cand < lo {
			cand = m.prev[cand%int64(m.win.Cap())]
			continue
		}
		l := 0
		for l < len(follow) {
			var cb byte
			if cand+int64(l) >= m.win.End() {
				break // candidate bytes must be real history, never future output
			}
			cb = m.win.At(cand + int64(l))
			if cb != follow[l] {
				break
			}
			l++
		}
		if l > length {
			length = l
			dist = pos - cand
			if l == len(follow) {
				break
			}
		}
		cand = m.prev[cand%int64(m.win.Cap())]
	}
	return dist, length
}
