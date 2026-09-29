// Package match finds longest LZ77 matches with a bounded hash chain.
package match

import "ontology/window"

// MinMatch is the shortest match emitted as a back-reference.
const MinMatch = 3

// Matcher owns the hash chains of one compression stream or block.
type Matcher struct {
	w        *window.Window
	winCap   int
	maxChain int
	head     []int
	prev     []int
	pos      int
	ingested int
	examined int
}

// New builds a matcher over a window with the candidate-chain cap.
func New(w *window.Window, maxChain int) *Matcher {
	if w == nil || w.Cap() <= 0 {
		panic("match: nil or invalid window")
	}
	if maxChain <= 0 {
		panic("match: maxChain must be positive")
	}
	m := &Matcher{w: w, winCap: w.Cap(), maxChain: maxChain}
	m.head = make([]int, 1<<16)
	m.prev = make([]int, m.winCap)
	for i := range m.head {
		m.head[i] = -1
	}
	return m
}

// Examined reports the total candidate positions inspected so far.
func (m *Matcher) Examined() int { return m.examined }

func hash3(b0, b1, b2 byte) int {
	return int(b0) | int(b1)<<8 | int(b2)
}

func (m *Matcher) add(abs int, b0, b1, b2 byte) {
	h := hash3(b0, b1, b2)
	m.prev[((abs%m.winCap)+m.winCap)%m.winCap] = m.head[h]
	m.head[h] = abs
}

func (m *Matcher) byteAt(abs int) byte { return m.w.At(m.pos - abs) }

// Preset installs a pre-existing dictionary (e.g. the previous block).
func (m *Matcher) Preset(data []byte) {
	for i := range m.head {
		m.head[i] = -1
	}
	if len(data) > m.winCap {
		data = data[len(data)-m.winCap:]
	}
	m.pos = len(data)
	for _, b := range data {
		m.w.Push(b)
	}
	for s := 0; s+2 < len(data); s++ {
		m.add(s, data[s], data[s+1], data[s+2])
	}
	m.ingested = m.pos - 2
	if m.ingested < 0 {
		m.ingested = 0
	}
}

// Ingest appends bytes and indexes every newly completed 3-byte triple.
func (m *Matcher) Ingest(data []byte) {
	start := m.pos
	for _, b := range data {
		m.w.Push(b)
		m.pos++
	}
	for s := m.ingested; s+2 < m.pos; s++ {
		if s < start-2 {
			continue
		}
		m.add(s, m.byteAt(s), m.byteAt(s+1), m.byteAt(s+2))
	}
	m.ingested = m.pos
}

// Find returns distance/length of the longest match at absolute position p.
// limit caps match length and must not exceed the bytes available from p.
func (m *Matcher) Find(p, limit int) (dist, length int) {
	if limit > m.pos-p {
		limit = m.pos - p
	}
	if limit < MinMatch {
		return 0, 0
	}
	h := hash3(m.byteAt(p), m.byteAt(p+1), m.byteAt(p+2))
	best := MinMatch - 1
	cand := m.head[h]
	for steps := 0; steps < m.maxChain && cand >= 0; steps++ {
		d := p - cand
		if d <= 0 || d > m.winCap {
			break
		}
		m.examined++
		l := 0
		for l < limit && m.byteAt(cand+l) == m.byteAt(p+l) {
			l++
		}
		if l > best {
			best, dist, length = l, d, l
			if l == limit {
				break
			}
		}
		cand = m.prev[((cand%m.winCap)+m.winCap)%m.winCap]
	}
	return dist, length
}
