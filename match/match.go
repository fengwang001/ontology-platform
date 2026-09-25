// Package match is a hash-chain longest-match finder over a sliding
// window. Chain walks are capped; the number of examined candidates is
// kept in an unexported counter.
package match

import "errors"

var ErrBadConfig = errors.New("match: window and chain must be positive")

const MinMatch = 3

type Matcher struct {
	win   int
	chain int
	head  map[uint32]int
	prev  []int // prev[pos%win] = previous position with the same hash, -1 if none
	cand  int64
}

func New(win, chain int) (*Matcher, error) {
	if win <= 0 || chain <= 0 {
		return nil, ErrBadConfig
	}
	return &Matcher{win: win, chain: chain, head: make(map[uint32]int), prev: make([]int, win)}, nil
}

// Candidates reports how many candidate positions have been examined.
func (m *Matcher) Candidates() int64 { return m.cand }

func hash(a, b, c byte) uint32 { return uint32(a)<<16 | uint32(b)<<8 | uint32(c) }

// Insert adds position pos to the chains. at(i) must yield the byte at
// absolute position i; total is the end of valid positions.
func (m *Matcher) Insert(at func(int) byte, pos, total int) {
	if pos+MinMatch > total {
		return
	}
	h := hash(at(pos), at(pos+1), at(pos+2))
	if p, ok := m.head[h]; ok {
		m.prev[pos%m.win] = p
	} else {
		m.prev[pos%m.win] = -1
	}
	m.head[h] = pos
}

// Longest finds the longest match at absolute position pos, looking back
// at most win bytes and examining at most chain candidates. maxLen caps
// the match length. Returns (0, 0) when no match of MinMatch exists.
func (m *Matcher) Longest(at func(int) byte, pos, maxLen int) (dist, ln int) {
	if maxLen < MinMatch {
		return 0, 0
	}
	c, ok := m.head[hash(at(pos), at(pos+1), at(pos+2))]
	if !ok {
		return 0, 0
	}
	best := 0
	for k := 0; c >= 0 && k < m.chain; k++ {
		d := pos - c
		if d > m.win {
			break
		}
		m.cand++
		n := 0
		for n < maxLen && at(c+n) == at(pos+n) {
			n++
		}
		if n > best {
			best, dist = n, d
			if best == maxLen {
				break
			}
		}
		c = m.prev[c%m.win]
	}
	if best < MinMatch {
		return 0, 0
	}
	return dist, best
}
