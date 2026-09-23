// Package match finds the longest match in the window with bounded hash chains.
// It depends only on the window package.
package match

import (
	"errors"

	"ontology/window"
)

// ErrConfig is returned when maxChain or window capacity is non-positive.
var ErrConfig = errors.New("match: window and maxChain must be positive")

const (
	hashBits = 16
	hashSize = 1 << hashBits
	minMatch = 3
	nilPos   = -1
)

// Matcher keeps a hash chain over every inserted absolute position.
type Matcher struct {
	win      *window.Window
	maxChain int
	maxMatch int
	head     []int // newest position per hash bucket
	next     []int // ring of previous-position links
	hashes   []uint32
	total    int // monotonic count of inserted bytes
	examined int64
}

// New builds a Matcher over a window. maxMatch caps reported match lengths.
func New(win *window.Window, maxChain, maxMatch int) (*Matcher, error) {
	if win == nil || maxChain <= 0 || maxMatch < minMatch {
		return nil, ErrConfig
	}
	m := &Matcher{
		win:      win,
		maxChain: maxChain,
		maxMatch: maxMatch,
		head:     make([]int, hashSize),
		next:     make([]int, win.Cap()),
		hashes:   make([]uint32, win.Cap()),
	}
	for i := range m.head {
		m.head[i] = nilPos
	}
	for i := range m.next {
		m.next[i] = nilPos
	}
	return m, nil
}

// Examined returns the total number of candidate positions ever inspected.
func (m *Matcher) Examined() int64 { return m.examined }

// Reset clears all chains and the underlying window.
func (m *Matcher) Reset() {
	for i := range m.head {
		m.head[i] = nilPos
	}
	for i := range m.next {
		m.next[i] = nilPos
	}
	m.win.Reset()
	m.total = 0
}

func hash3(b0, b1, b2 byte) uint32 {
	return (uint32(b0)<<10 ^ uint32(b1)<<5 ^ uint32(b2)) & (hashSize - 1)
}

// Insert appends one byte, and when a 3-byte triple is complete links its
// absolute position into that triple's hash chain.
func (m *Matcher) Insert(c byte) {
	t := m.total
	m.win.AppendAll([]byte{c})
	if t >= 2 {
		h := hash3(m.win.At(3), m.win.At(2), m.win.At(1))
		slot := t % m.win.Cap()
		m.hashes[slot] = h
		m.next[slot] = m.head[h]
		m.head[h] = t
	}
	m.total++
}

func (m *Matcher) srcByte(l, dist int, future []byte) byte {
	if l < dist {
		return m.win.At(dist - l) // candidate byte still inside committed window
	}
	return future[l-dist] // candidate byte lives in the uncommitted tail (overlap)
}

// Find searches the chain for the longest match starting at future[0], limited
// by maxLen (bytes available) and maxMatch. Returns distance and length;
// length 0 means no match of at least minMatch.
func (m *Matcher) Find(future []byte, maxLen int) (int, int) {
	if maxLen > len(future) {
		maxLen = len(future)
	}
	if maxLen < minMatch || len(future) < minMatch {
		return 0, 0
	}
	if maxLen > m.maxMatch {
		maxLen = m.maxMatch
	}
	h := hash3(future[0], future[1], future[2])
	pos := m.head[h]
	bestDist, bestLen := 0, minMatch-1
	for k := 0; k < m.maxChain && pos != nilPos; k++ {
		m.examined++
		dist := m.total - pos
		if dist <= 0 || dist > m.win.Len() {
			break // stale slot or out of reachable history
		}
		limit := dist + len(future) - 1 // source may run through the whole tail
		if limit > maxLen {
			limit = maxLen
		}
		if limit > bestLen { // cannot beat current best: skip comparison
			l := 0
			for l < limit && m.srcByte(l, dist, future) == future[l] {
				l++
			}
			if l > bestLen {
				bestLen, bestDist = l, dist
				if bestLen >= maxLen {
					break
				}
			}
		}
		pos = m.next[pos%m.win.Cap()]
	}
	if bestLen < minMatch {
		return 0, 0
	}
	return bestDist, bestLen
}
