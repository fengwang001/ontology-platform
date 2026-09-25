// Package match implements a hash-chain longest-match finder over a
// sliding window. The candidate chain walked per query is bounded.
package match

import (
	"errors"

	"ontology/window"
)

// ErrZeroChain rejects a non-positive chain length limit.
var ErrZeroChain = errors.New("match: chain length limit must be positive")

const (
	hashBits = 16
	hashLen  = 4
)

// Matcher finds matches in a window. It is not safe for concurrent use.
type Matcher struct {
	w     *window.Window
	head  []int
	prev  []int
	chain int
	cand  int
}

// New creates a matcher over w examining at most maxChain candidates per Find.
func New(w *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrZeroChain
	}
	head := make([]int, 1<<hashBits)
	for i := range head {
		head[i] = -1
	}
	return &Matcher{w: w, head: head, prev: make([]int, w.Cap()), chain: maxChain}, nil
}

// hash reads 4 bytes at pos; callers guarantee pos+hashLen <= w.Total().
func (m *Matcher) hash(pos int) int {
	x := uint32(m.w.At(pos)) | uint32(m.w.At(pos+1))<<8 |
		uint32(m.w.At(pos+2))<<16 | uint32(m.w.At(pos+3))<<24
	return int((x * 2654435761) >> (32 - hashBits))
}

// Insert adds absolute position pos to the hash chains.
func (m *Matcher) Insert(pos int) {
	h := m.hash(pos)
	m.prev[pos%m.w.Cap()] = m.head[h]
	m.head[h] = pos
}

// Find returns the longest match at absolute position pos, considering only
// candidates still inside the window and at most m.chain of them.
func (m *Matcher) Find(pos, maxLen int) (dist, length int) {
	oldest := max(m.w.Total()-m.w.Cap(), 0)
	budget := m.chain
	for c := m.head[m.hash(pos)]; c >= oldest && budget > 0; c = m.prev[c%m.w.Cap()] {
		m.cand++
		budget--
		l := 0
		for l < maxLen && m.w.At(c+l) == m.w.At(pos+l) {
			l++
		}
		if l > length {
			length, dist = l, pos-c
			if l == maxLen {
				break
			}
		}
	}
	return dist, length
}

// Candidates returns the total number of candidate positions examined so far.
func (m *Matcher) Candidates() int { return m.cand }
