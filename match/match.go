// Package match finds longest matches with a bounded hash chain.
package match

import "ontology/window"

const MinMatch = 3

// Matcher is a hash-chain matcher backed by a sliding window.
type Matcher struct {
	win        *window.Window
	head       []int
	prev       []int
	chainLimit int
	examined   int64
}

// New constructs a Matcher over win. chainLimit must be positive.
func New(win *window.Window, chainLimit int) *Matcher {
	if chainLimit <= 0 {
		panic("match: chainLimit must be positive")
	}
	return &Matcher{
		win:        win,
		head:       make([]int, 1<<16),
		prev:       make([]int, win.Cap()+1),
		chainLimit: chainLimit,
	}
}

func hash3(b0, b1, b2 byte) uint32 {
	h := uint32(2166136261)
	h = (h ^ uint32(b0)) * 16777619
	h = (h ^ uint32(b1)) * 16777619
	h = (h ^ uint32(b2)) * 16777619
	return h & 0xffff
}

func (m *Matcher) slot(pos int) int {
	s := pos % (m.win.Cap() + 1)
	if s < 0 {
		s += m.win.Cap() + 1
	}
	return s
}

// Insert puts one candidate position (1-based absolute index) into the chain.
func (m *Matcher) Insert(pos int, b0, b1, b2 byte) {
	h := hash3(b0, b1, b2)
	m.prev[m.slot(pos)] = m.head[h]
	m.head[h] = pos
}

// InsertRange inserts eligible positions for bytes about to enter the window.
func (m *Matcher) InsertRange(startPos int, data []byte) {
	for i := 0; i+2 < len(data); i++ {
		m.Insert(startPos+i, data[i], data[i+1], data[i+2])
	}
}

// Find returns the longest match length for data starting at the given
// absolute position, bounded by maxLen. Distance is relative to data[0].
func (m *Matcher) Find(pos int, data []byte, maxLen int) (dist, length int) {
	if len(data) < MinMatch || maxLen < MinMatch {
		return 0, 0
	}
	if maxLen > len(data) {
		maxLen = len(data)
	}
	h := hash3(data[0], data[1], data[2])
	cand := m.head[h]
	for steps := 0; steps < m.chainLimit; steps++ {
		d := pos - cand
		if cand <= 0 || d > m.win.Cap() {
			break
		}
		m.examined++
		limit := maxLen
		if avail := m.win.Len() - (cand - 1); avail < limit {
			limit = avail
		}
		l := 0
		for l < limit && m.win.At(cand+l) == data[l] {
			l++
		}
		if l > length {
			dist, length = d, l
			if length == maxLen {
				return
			}
		}
		next := m.prev[m.slot(cand)]
		if next <= 0 || next >= cand {
			break
		}
		cand = next
	}
	return
}

// Examined reports how many candidate positions were inspected.
func (m *Matcher) Examined() int64 { return m.examined }
