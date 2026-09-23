package match

import (
	"errors"

	"ontology/window"
)

var ErrInvalidConfig = errors.New("match: max chain must be positive")

type Matcher struct {
	heads     []int32
	chains    []int32
	source    []byte
	base      int
	nextPos   int
	windowCap int
	chainMax  int
	examined  int64
	minMatch  int
	maxMatch  int
}

func New(win *window.Window, maxChain, minMatch, maxMatch int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Matcher{
		heads: func() []int32 {
			h := make([]int32, 1<<16)
			for i := range h {
				h[i] = -1
			}
			return h
		}(),
		chains:    make([]int32, win.Capacity()),
		windowCap: win.Capacity(),
		chainMax:  maxChain,
		minMatch:  minMatch,
		maxMatch:  maxMatch,
	}, nil
}

func (m *Matcher) CandidatesExamined() int64 { return m.examined }

func (m *Matcher) Use(source []byte, history int) {
	m.source = source
	if history == m.nextPos {
		return
	}
	for i := range m.heads {
		m.heads[i] = -1
	}
	m.nextPos = 0
	for m.nextPos < history && m.nextPos+3 <= len(source) {
		m.insertAt(m.nextPos)
	}
	m.nextPos = history
}

func (m *Matcher) Find(pos int) (distance, length int) {
	data := m.source
	if pos+m.minMatch > len(data) {
		return 0, 0
	}
	return m.findAt(pos)
}

func (m *Matcher) findAt(pos int) (distance, length int) {
	data := m.source
	h := hash(data[pos:])
	candidate := int(m.heads[h])
	oldest := m.nextPos - m.windowCap
	for tries := 0; tries < m.chainMax && candidate >= oldest && candidate >= 0; tries++ {
		m.examined++
		n := m.matchLen(data, pos, candidate)
		if n >= m.minMatch && n > length {
			distance, length = pos-candidate, n
			if n == m.maxMatch {
				break
			}
		}
		next := int(m.chains[candidate%len(m.chains)])
		if next >= candidate {
			break
		}
		candidate = next
	}
	m.insertAt(pos - m.base)
	return distance, length
}

func (m *Matcher) Advance(pos int) {
	for m.nextPos < pos {
		m.insertAt(m.nextPos)
	}
}

func (m *Matcher) insertAt(pos int) {
	if pos+3 > len(m.source) {
		return
	}
	h := hash(m.source[pos:])
	slot := pos % len(m.chains)
	if m.heads[h] >= 0 {
		m.chains[slot] = m.heads[h]
	} else {
		m.chains[slot] = -1
	}
	m.heads[h] = int32(pos)
	if pos >= m.nextPos {
		m.nextPos = pos + 1
	}
}

func (m *Matcher) matchLen(data []byte, pos, cand int) int {
	limit := len(data) - pos
	if limit > m.maxMatch {
		limit = m.maxMatch
	}
	n := 0
	for n < limit {
		b := data[pos+n]
		c := data[cand+n]
		if b != c {
			break
		}
		n++
	}
	return n
}

func hash(p []byte) uint32 {
	return (uint32(p[0]) | uint32(p[1])<<8 | uint32(p[2])<<13) & 0xffff
}
