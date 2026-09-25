package match

import (
	"errors"

	"ontology/window"
)

const hashSize = 1 << 16
const MaxLength = 1 << 16

type Matcher struct {
	window     *window.Window
	maxChain   int
	last       [hashSize]int
	prev       []int
	prevPos    []int
	candidates int
}

func New(w *window.Window, maxChain int) (*Matcher, error) {
	if w == nil {
		return nil, errors.New("match: nil window")
	}
	if maxChain <= 0 {
		return nil, errors.New("match: chain limit must be positive")
	}
	m := &Matcher{window: w, maxChain: maxChain, prev: make([]int, w.Cap()), prevPos: make([]int, w.Cap())}
	for i := range m.last {
		m.last[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
		m.prevPos[i] = -1
	}
	return m, nil
}

func hash3(p []byte) int {
	return int(p[0])<<12 ^ int(p[1])<<6 ^ int(p[2])
}

func (m *Matcher) Find(p []byte) (distance, length int) {
	if len(p) < 3 {
		return 0, 0
	}
	start := m.window.Total()
	capacity := m.window.Cap()
	best := 2
	limit := len(p)
	if limit > MaxLength {
		limit = MaxLength
	}
	candidate := m.last[hash3(p)]
	for examined := 0; examined < m.maxChain && candidate >= 0 && start-candidate <= capacity; examined++ {
		m.candidates++
		d := start - candidate
		n := 0
		for n < limit {
			var b byte
			if n < d {
				b, _ = m.window.Byte(d - n)
			} else {
				b = p[n-d]
			}
			if b != p[n] {
				break
			}
			n++
		}
		if n > best {
			best, distance, length = n, d, n
		}
		next := m.prev[candidate%capacity]
		if m.prevPos[candidate%capacity] != candidate {
			next = -1
		}
		candidate = next
	}
	return distance, length
}

func (m *Matcher) Prepare(dict, head []byte) {
	m.window.AddBytes(dict)
	start := m.window.Total() - len(dict)
	capacity := m.window.Cap()
	for i := 0; i < len(dict); i++ {
		if i+3 > len(dict)+len(head) {
			continue
		}
		var tri [3]byte
		for j := range tri {
			if i+j < len(dict) {
				tri[j] = dict[i+j]
			} else {
				tri[j] = head[i+j-len(dict)]
			}
		}
		pos := start + i
		slot := pos % capacity
		m.prevPos[slot] = pos
		m.prev[slot] = m.last[hash3(tri[:])]
		m.last[hash3(tri[:])] = pos
	}
}

func (m *Matcher) Insert(p []byte) {
	if len(p) == 0 {
		return
	}
	if len(p) < 3 {
		m.window.Add(p[0])
		return
	}
	start := m.window.Total()
	h := hash3(p)
	slot := start % m.window.Cap()
	m.prevPos[slot] = start
	m.prev[slot] = m.last[h]
	m.last[h] = start
	m.window.Add(p[0])
}

func (m *Matcher) Candidates() int { return m.candidates }

func (m *Matcher) ResetCandidates() { m.candidates = 0 }
