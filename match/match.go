package match

import (
	"errors"

	"ontology/window"
)

var ErrInvalidChain = errors.New("match chain limit must be positive")

type Matcher struct {
	win      *window.Window
	head     []int
	previous []int
	position int
	maxChain int
	examined int64
}

func New(capacity, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrInvalidChain
	}
	w, err := window.New(capacity)
	if err != nil {
		return nil, err
	}
	m := &Matcher{
		win:      w,
		head:     make([]int, capacity),
		previous: make([]int, capacity),
		maxChain: maxChain,
	}
	m.clearChains()
	return m, nil
}

func (m *Matcher) Window() *window.Window { return m.win }
func (m *Matcher) CandidateCount() int64  { return m.examined }
func (m *Matcher) ResetCandidates()       { m.examined = 0 }

func (m *Matcher) clearChains() {
	for i := range m.head {
		m.head[i] = -1
		m.previous[i] = -1
	}
}

func (m *Matcher) Seed(prefix []byte) {
	m.clearChains()
	m.win = mustWindow(m.win.Capacity())
	m.position = len(prefix)
	m.win.Add(prefix)
	start := len(prefix) - m.win.Capacity()
	if start < 0 {
		start = 0
	}
	for p := start; p+3 <= len(prefix); p++ {
		m.insert(p, prefix[p:p+3])
	}
}

func mustWindow(capacity int) *window.Window {
	w, err := window.New(capacity)
	if err != nil {
		panic(err)
	}
	return w
}

func (m *Matcher) Add(data []byte) {
	if len(data) == 0 {
		return
	}
	m.win.Add(data)
	for i := 0; i+3 <= len(data); i++ {
		m.insert(m.position+i, data[i:i+3])
	}
	m.position += len(data)
}

func (m *Matcher) insert(position int, key []byte) {
	h := hash(key) % len(m.head)
	m.previous[position%cap(m.head)] = m.head[h]
	m.head[h] = position
}

func (m *Matcher) Find(data []byte, at int) (distance, length int) {
	if at+3 > len(data) {
		return 0, 0
	}
	key := data[at : at+3]
	h := hash(key) % len(m.head)
	candidate := m.head[h]
	current := m.position + at
	remaining := len(data) - at
	for steps := 0; steps < m.maxChain && candidate >= 0; steps++ {
		d := current - candidate
		if d <= 0 || d > m.win.Len() {
			break
		}
		m.examined++
		n := 0
		for n < remaining {
			var b byte
			if n < d {
				b, _ = m.win.At(d - n)
			} else {
				b = data[at+n-d]
			}
			if b != data[at+n] {
				break
			}
			n++
		}
		if n > length {
			distance, length = d, n
			if n == remaining {
				break
			}
		}
		next := m.previous[candidate%len(m.head)]
		if next >= candidate {
			break
		}
		candidate = next
	}
	return distance, length
}

func hash(key []byte) int {
	return int(uint32(key[0])<<16 | uint32(key[1])<<8 | uint32(key[2]))
}
