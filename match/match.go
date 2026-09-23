// Package match implements a hash-chain longest-match finder over a window.
package match

import (
	"errors"

	"ontology/window"
)

const (
	// MinMatch is the shortest match worth emitting.
	MinMatch = 3
	hashSize = 1 << 16
	hashMask = hashSize - 1
)

var ErrConfig = errors.New("match: capacity and chain limit must be > 0")

// Matcher is a hash-chain matcher over a fixed sliding window.
// A single instance is not safe for concurrent use.
type Matcher struct {
	win      *window.Window
	chain    int
	total    int64 // number of bytes ever inserted
	head     []int64
	prevPos  []int64
	prevLink []int64
	examined int64 // non-exported candidate-examination counter
}

// New builds a matcher; capacity is window size, chain caps examined candidates.
func New(capacity, chain int) (*Matcher, error) {
	if capacity <= 0 || chain <= 0 {
		return nil, ErrConfig
	}
	w, err := window.New(capacity)
	if err != nil {
		return nil, err
	}
	return &Matcher{
		win:      w,
		chain:    chain,
		head:     make([]int64, hashSize),
		prevPos:  make([]int64, capacity),
		prevLink: make([]int64, capacity),
	}, nil
}

// Cap returns the window capacity.
func (m *Matcher) Cap() int { return m.win.Cap() }

// Win returns the underlying window (used for preset dictionaries/inspection).
func (m *Matcher) Win() *window.Window { return m.win }

// Candidates returns the cumulative number of examined candidate positions.
func (m *Matcher) Candidates() int64 { return m.examined }

func hash3(p []byte) uint32 {
	return (uint32(p[0]) | uint32(p[1])<<8 | uint32(p[2])<<16) & hashMask
}

// Insert appends committed bytes, updating the window and hash chains.
func (m *Matcher) Insert(p []byte) {
	for _, b := range p {
		m.insertByte(b)
	}
}

func (m *Matcher) insertByte(b byte) {
	m.win.Write([]byte{b})
	t := m.total
	m.total++
	if t < MinMatch-1 {
		return
	}
	h := uint32(m.win.At(3)) | uint32(m.win.At(2))<<8 | uint32(m.win.At(1))<<16
	h &= hashMask
	slot := t % int64(m.win.Cap())
	m.prevPos[slot] = t
	m.prevLink[slot] = m.head[h]
	m.head[h] = t + 1
}

// Match finds the longest match for pending (uncommitted) bytes in history,
// bounded by maxLen bytes. distance 0 means no match; else length >= MinMatch.
func (m *Matcher) Match(pending []byte, maxLen int) (distance, length int) {
	if len(pending) < MinMatch || m.win.Len() < MinMatch {
		return 0, 0
	}
	h := hash3(pending)
	limit := int64(m.win.Cap())
	if maxLen > len(pending) {
		maxLen = len(pending)
	}
	if maxLen > m.win.Cap() {
		maxLen = m.win.Cap()
	}
	cand := m.head[h] - 1
	for n := 0; n < m.chain && cand >= 0; n++ {
		m.examined++
		dist := m.total - cand
		if dist < 1 || dist > limit {
			break
		}
		k := 0
		for k < maxLen && pending[k] == m.win.At(int(dist)+k) {
			k++
		}
		if k > length {
			distance, length = int(dist), k
			if k == maxLen {
				break
			}
		}
		next := m.prevLink[cand%int64(m.win.Cap())]
		if next <= 0 || next-1 >= cand {
			break
		}
		cand = next - 1
	}
	if length < MinMatch {
		return 0, 0
	}
	return distance, length
}
