// Package match finds longest matches with a bounded hash chain over a window.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest back-reference the matcher emits.
const MinMatch = 3

// ErrInvalidConfig is returned for non-positive chain limits.
var ErrInvalidConfig = errors.New("match: maxChain must be > 0")

const (
	hashBits = 15
	hashSize = 1 << hashBits
	hashMul  = 2654435761
	none     = -1
)

// Matcher is single-goroutine; it owns one window and one block's search state.
type Matcher struct {
	win      *window.Window
	head     []int32
	prev     []int32
	maxChain int
	blk      []byte // current block bytes (dictionary bytes live only in win)
	base     int    // absolute position of blk[0]
	examined int64  // total candidate positions inspected (unexported)
}

// New constructs a matcher over win; win may already hold a preset dictionary.
func New(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrInvalidConfig
	}
	head := make([]int32, hashSize)
	for i := range head {
		head[i] = none
	}
	return &Matcher{
		win:      win,
		head:     head,
		prev:     make([]int32, win.Cap()),
		maxChain: maxChain,
	}, nil
}

// Reset starts matching blk; bytes already in the window act as a dictionary.
func (m *Matcher) Reset(blk []byte) {
	m.blk = blk
	m.base = m.win.Len()
}

// Examined reports the cumulative number of candidate positions inspected.
func (m *Matcher) Examined() int64 { return m.examined }

func hash3(p []byte) uint32 {
	return (uint32(p[0])<<16 + uint32(p[1])<<8 + uint32(p[2])) * hashMul >> (32 - hashBits)
}

// absByte reads one byte by absolute stream position (window or block).
func (m *Matcher) absByte(abs, cursor int) byte {
	if abs < m.base {
		return m.win.ByteAt(cursor - abs)
	}
	return m.blk[abs-m.base]
}

// Find returns the longest match starting at block offset pos. Distance is
// 1-based; length 0 means no usable match.
func (m *Matcher) Find(pos int) (distance, length int) {
	if pos+MinMatch > len(m.blk) {
		return 0, 0
	}
	cursor := m.base + pos
	maxLen := len(m.blk) - pos
	h := hash3(m.blk[pos:])
	cand := m.head[h]
	for i := 0; i < m.maxChain && cand != none; i++ {
		c := int(cand)
		d := cursor - c
		m.examined++
		if d > m.win.Cap() || d > cursor {
			break // chain is newest-first; older candidates are even farther
		}
		j := 0
		for j < maxLen && m.absByte(c+j, cursor+j) == m.blk[pos+j] {
			j++
		}
		if j > length && j >= MinMatch {
			distance, length = d, j
		}
		if j == maxLen {
			break // no later candidate can be longer
		}
		cand = m.prev[int32(c)&int32(m.win.Cap()-1)]
	}
	return distance, length
}

// Accept inserts hashes for block bytes [start, end) and pushes them to the
// window. Both literal runs and matched spans are inserted exactly once.
func (m *Matcher) Accept(start, end int) {
	for i := start; i < end; i++ {
		if i+MinMatch <= len(m.blk) {
			abs := int32(m.base + i)
			h := hash3(m.blk[i:])
			m.prev[abs&int32(m.win.Cap()-1)] = m.head[h]
			m.head[h] = abs
		}
		m.win.Push(m.blk[i])
	}
}
