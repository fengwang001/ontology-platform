// Package match finds the longest match inside a sliding window via hash chains.
package match

import "ontology/window"

// MinMatch is the shortest back-reference length emitted.
const MinMatch = 3

// Matcher is a hash-chain longest-match finder.
type Matcher struct {
	win        *window.Window
	chainLimit int
	head       []int // latest absolute position for each hash
	next       []int // previous occurrence, indexed by pos % cap
	examined   int64
}

// New builds a Matcher; windowCap and chainLimit must be positive.
func New(windowCap, chainLimit int) *Matcher {
	if windowCap <= 0 || chainLimit <= 0 {
		panic("match: windowCap and chainLimit must be positive")
	}
	m := &Matcher{
		win:        window.New(windowCap),
		chainLimit: chainLimit,
		head:       make([]int, 1<<16),
		next:       make([]int, windowCap),
	}
	for i := range m.head {
		m.head[i] = -1
	}
	return m
}

// Win exposes the backing window (used by the encoder to append bytes).
func (m *Matcher) Win() *window.Window { return m.win }

// Reset clears history and inserts dict as a preset dictionary.
func (m *Matcher) Reset(dict []byte) {
	for i := range m.head {
		m.head[i] = -1
	}
	m.win.Reset()
	m.win.Append(dict)
	pos := 0
	if len(dict) > m.win.Cap() {
		pos = len(dict) - m.win.Cap()
	}
	for ; pos+MinMatch <= len(dict); pos++ {
		m.insert(m.hashAt(pos), pos)
	}
	m.examined = 0
}

// Examined returns the total number of candidate positions inspected.
func (m *Matcher) Examined() int64 { return m.examined }

// Insert registers the absolute position pos (which must already be in the window).
func (m *Matcher) Insert(pos int) {
	if pos+MinMatch > m.win.Len() {
		return
	}
	m.insert(m.hashAt(pos), pos)
}

// Find returns the longest match at absolute position pos, limited to maxLen.
// A zero length means no match of at least MinMatch bytes.
func (m *Matcher) Find(pos, maxLen int) (dist, length int) {
	if pos+MinMatch > m.win.Len() || maxLen < MinMatch {
		return 0, 0
	}
	oldest := pos - m.win.Cap()
	cand := m.head[m.hashAt(pos)]
	for n := 0; n < m.chainLimit && cand > oldest; n++ {
		m.examined++
		l := m.extend(cand, pos, maxLen)
		if l > length {
			dist, length = pos-cand, l
			if l == maxLen {
				break
			}
		}
		cand = m.next[cand%len(m.next)]
		if cand < 0 {
			break
		}
	}
	return dist, length
}

func (m *Matcher) insert(h uint32, pos int) {
	m.next[pos%len(m.next)] = m.head[h]
	m.head[h] = pos
}

func (m *Matcher) hashAt(pos int) uint32 {
	return uint32(m.win.At(pos))<<10 ^
		uint32(m.win.At(pos+1))<<5 ^
		uint32(m.win.At(pos+2))
}

func (m *Matcher) extend(cand, pos, maxLen int) int {
	limit := pos + maxLen
	if end := m.win.Len(); limit > end {
		limit = end
	}
	l := 0
	for pos+l < limit && m.win.At(cand+l) == m.win.At(pos+l) {
		l++
	}
	return l
}
