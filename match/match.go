// Package match finds longest matches within a sliding window using
// hash chains with a bounded candidate chain length.
package match

import "errors"

// ErrBadConfig rejects non-positive capacity or chain length.
var ErrBadConfig = errors.New("match: capacity and chain must be positive")

const (
	hashBits = 15
	hashSize = 1 << hashBits
	minMatch = 3
)

// MinMatch is the minimum backref length emitted by compressors.
func MinMatch() int { return minMatch }

// Source provides random access to the byte stream by absolute position.
type Source interface {
	ByteAt(pos int64) (byte, bool)
}

// Matcher keeps hash chains over positions in a window of cap bytes.
type Matcher struct {
	src   Source
	cap   int64
	chain int
	head  []int64 // hash -> pos+1, 0 = empty
	prev  []int64 // ring by pos % cap: pos+1 of previous same-hash position
	cand  int64   // total candidate positions examined (unexported counter)
}

// New creates a matcher reading bytes from src.
func New(src Source, capacity, chain int) (*Matcher, error) {
	if capacity <= 0 || chain <= 0 {
		return nil, ErrBadConfig
	}
	return &Matcher{
		src:   src,
		cap:   int64(capacity),
		chain: chain,
		head:  make([]int64, hashSize),
		prev:  make([]int64, capacity),
	}, nil
}

// Candidates returns the total number of candidate positions examined.
func (m *Matcher) Candidates() int64 { return m.cand }

func (m *Matcher) hash(pos int64) (int64, bool) {
	b0, ok0 := m.src.ByteAt(pos)
	b1, ok1 := m.src.ByteAt(pos + 1)
	b2, ok2 := m.src.ByteAt(pos + 2)
	if !ok0 || !ok1 || !ok2 {
		return 0, false
	}
	h := (int64(b0)*251 + int64(b1)) * 251 + int64(b2)
	return h & (hashSize - 1), true
}

// Insert registers pos in the hash chains. It is a no-op when fewer
// than minMatch bytes are available at pos.
func (m *Matcher) Insert(pos int64) {
	h, ok := m.hash(pos)
	if !ok {
		return
	}
	slot := pos % m.cap
	m.prev[slot] = m.head[h]
	m.head[h] = pos + 1
}

// Find returns the distance and length of the longest match at pos,
// considering only positions within the window, at most chain candidates
// and at most maxLen bytes. Overlapping matches (dist < length) are
// allowed because the source serves bytes written by the match itself.
func (m *Matcher) Find(pos int64, maxLen int) (dist, length int) {
	h, ok := m.hash(pos)
	if !ok {
		return 0, 0
	}
	best, bestDist := 0, 0
	for p, steps := m.head[h], 0; p != 0 && steps < m.chain; steps++ {
		p-- // stored as pos+1
		if p >= pos {
			break
		}
		if pos-p > m.cap {
			break
		}
		m.cand++
		l := 0
		for l < maxLen {
			a, okA := m.src.ByteAt(pos + int64(l))
			if !okA {
				break
			}
			b, okB := m.src.ByteAt(p + int64(l))
			if !okB || a != b {
				break
			}
			l++
		}
		if l > best {
			best, bestDist = l, int(pos-p)
			if best >= maxLen {
				break
			}
		}
		p = m.prev[p%m.cap]
	}
	return bestDist, best
}
