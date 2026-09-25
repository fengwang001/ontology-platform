// Package match implements a hash-chain longest-match searcher over a
// sliding window. The number of chain candidates examined per position
// is bounded by a configurable cap.
package match

import "errors"

// MaxLen caps a single match so deferred decisions stay bounded.
const MaxLen = 1024

const hashBits = 15

// Matcher keeps hash chains of processed positions.
type Matcher struct {
	head     []int64 // hash -> newest position, -1 when empty
	prev     []int64 // pos % wcap -> previous position with same hash
	wcap     int64
	chainCap int
	examined int64 // total candidates ever examined (complexity proof)
}

// New returns a matcher; both windowCap and chainCap must be > 0.
func New(windowCap, chainCap int) (*Matcher, error) {
	if windowCap <= 0 {
		return nil, errors.New("match: window capacity must be > 0")
	}
	if chainCap <= 0 {
		return nil, errors.New("match: chain cap must be > 0")
	}
	head := make([]int64, 1<<hashBits)
	for i := range head {
		head[i] = -1
	}
	return &Matcher{head: head, prev: make([]int64, windowCap), wcap: int64(windowCap), chainCap: chainCap}, nil
}

// Hash3 hashes the three bytes starting a potential match.
func Hash3(a, b, c byte) uint32 {
	return (uint32(a)<<16 | uint32(b)<<8 | uint32(c)) * 0x9E3779B1 >> (32 - hashBits)
}

// Insert adds position pos (with hash h) to the chains.
func (m *Matcher) Insert(pos int64, h uint32) {
	m.prev[pos%m.wcap] = m.head[h]
	m.head[h] = pos
}

// Find returns the distance and length of the longest match at pos,
// capped at MaxLen and avail. at(abs) must yield the byte at any
// absolute position in [pos-wcap, pos+avail).
func (m *Matcher) Find(pos int64, h uint32, at func(int64) byte, avail int) (dist, length int) {
	if avail > MaxLen {
		avail = MaxLen
	}
	best, bestDist := 0, 0
	cand := m.head[h]
	for n := 0; n < m.chainCap && cand >= 0; n++ {
		if pos-cand > m.wcap {
			break
		}
		m.examined++
		l := 0
		for l < avail && at(cand+int64(l)) == at(pos+int64(l)) {
			l++
		}
		if l > best {
			best, bestDist = l, int(pos-cand)
			if l >= avail {
				break
			}
		}
		cand = m.prev[cand%m.wcap]
	}
	return bestDist, best
}

// Examined reports the total number of candidates examined so far.
func (m *Matcher) Examined() int64 { return m.examined }
