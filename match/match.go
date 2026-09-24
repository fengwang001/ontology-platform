// Package match finds longest matches in the sliding window via hash chains.
package match

import (
	"errors"

	"ontology/window"
)

const hashBits = 16

var ErrConfig = errors.New("match: chain limit must be positive")

// Matcher is a hash-chain longest-match finder.
type Matcher struct {
	win      *window.Window
	head     []int // hash bucket -> newest absolute position (-1 empty)
	next     []int // ring: previous candidate for absolute position (-1 none)
	chain    int   // max candidates examined per Find
	examined int64 // total candidate positions examined (unexported)
}

// New builds a matcher over an existing window with a candidate-chain cap.
func New(win *window.Window, chainLimit int) *Matcher {
	if chainLimit <= 0 {
		panic(ErrConfig)
	}
	m := &Matcher{
		win:   win,
		head:  make([]int, 1<<hashBits),
		next:  make([]int, win.Cap()),
		chain: chainLimit,
	}
	m.initHead()
	return m
}

func (m *Matcher) initHead() {
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.next {
		m.next[i] = -1
	}
}

func hash(b0, b1, b2 byte) int {
	return (int(b0)*131 + int(b1)*17 + int(b2)) & (1<<hashBits - 1)
}

// link registers absolute position p in the hash chain (3-byte hash).
func (m *Matcher) link(p int) {
	if p+2 >= m.win.Len() {
		return
	}
	h := hash(m.win.At(p), m.win.At(p+1), m.win.At(p+2))
	m.next[p%m.win.Cap()] = m.head[h]
	m.head[h] = p
}

// Find returns the longest match starting at absolute position p (capped at
// maxLen) as (distance, length); length 0 means no usable match. Candidates
// beyond win.Cap() behind p are ignored.
func (m *Matcher) Find(p, maxLen int) (dist, length int) {
	if p+2 >= m.win.Len() || maxLen < 3 {
		return 0, 0
	}
	h := hash(m.win.At(p), m.win.At(p+1), m.win.At(p+2))
	cand := m.head[h]
	limit := p - m.win.Cap()
	steps := 0
	for cand >= 0 && steps < m.chain {
		if cand <= limit {
			if cand == 0 {
				break
			}
			cand = m.next[cand%m.win.Cap()]
			continue
		}
		m.examined++
		steps++
		d := p - cand
		avail := m.win.Len() - cand
		if avail > maxLen {
			avail = maxLen
		}
		if avail > length {
			l := 0
			for l < avail && m.win.At(cand+l) == m.win.At(p+l) {
				l++
			}
			if l > length {
				length, dist = l, d
				if l == avail {
					break
				}
			}
		}
		if cand == 0 {
			return dist, length
		}
		cand = m.next[cand%m.win.Cap()]
	}
	return dist, length
}

// Insert links position p after Find.
func (m *Matcher) Insert(p int) { m.link(p) }

// Candidates returns the total candidate positions examined by Find.
func (m *Matcher) Candidates() int64 { return m.examined }
