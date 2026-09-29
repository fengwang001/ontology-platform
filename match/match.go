package match

import (
	"errors"

	"ontology/window"
)

var ErrInvalidChainLimit = errors.New("match: chain limit must be positive")

const MinLength = 3

type Matcher struct {
	capacity   int
	maxChain   int
	context    []byte
	offset     int
	heads      [65536]int
	chains     []int
	examined   int
	lastInsert int
}

func New(capacity, maxChain int) (*Matcher, error) {
	if capacity < MinLength {
		return nil, window.ErrInvalidCapacity
	}
	if maxChain <= 0 {
		return nil, ErrInvalidChainLimit
	}
	for i := range &m.heads {
		m.heads[i] = -1
	}
	return &Matcher{capacity: capacity, maxChain: maxChain}, nil
}

func (m *Matcher) Reset(dictionary, data []byte) {
	start := 0
	if len(dictionary) > m.capacity {
		start = len(dictionary) - m.capacity
	}
	m.context = append(dictionary[start:start:start+len(dictionary[start:])], data...)
	m.offset = len(dictionary[start:])
	m.chains = make([]int, len(m.context))
	for i := range m.chains {
		m.chains[i] = -1
	}
	for i := range &m.heads {
		m.heads[i] = -1
	}
	m.examined = 0
	m.lastInsert = m.offset - 1
	for pos := 0; pos < m.offset && pos+2 < len(m.context); pos++ {
		m.insert(pos)
	}
}

func (m *Matcher) CandidateCount() int { return m.examined }

func (m *Matcher) Match(pos int) (distance, length int) {
	current := m.offset + pos
	for m.lastInsert < current-1 {
		m.lastInsert++
		if m.lastInsert >= 0 && m.lastInsert+2 < len(m.context) {
			m.insert(m.lastInsert)
		}
	}
	if current+2 >= len(m.context) {
		return 0, 0
	}
	h := hash(m.context[current:])
	candidate := m.heads[h]
	for tried := 0; tried < m.maxChain && candidate >= 0; tried++ {
		m.examined++
		d := current - candidate
		if d > m.capacity {
			break
		}
		if n := m.matchLength(current, candidate); n > length {
			distance, length = d, n
		}
		candidate = m.chains[candidate]
	}
	return distance, length
}

func (m *Matcher) insert(pos int) {
	h := hash(m.context[pos:])
	m.chains[pos] = m.heads[h]
	m.heads[h] = pos
}

func (m *Matcher) matchLength(pos, candidate int) int {
	limit := len(m.context) - pos
	n := 0
	for n < limit && m.context[candidate+n] == m.context[pos+n] {
		n++
	}
	return n
}

func hash(p []byte) uint16 {
	return uint16(p[0]) | uint16(p[1])<<5 | uint16(p[2])<<10
}
