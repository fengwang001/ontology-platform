package match

import (
	"errors"

	"ontology/window"
)

var ErrInvalidConfig = errors.New("match: chain limit must be positive")

type Result struct {
	Distance int
	Length   int
}

type Matcher struct {
	window  *window.Window
	limit   int
	head    map[uint32]int
	prev    []int
	probes  int
	global  int
}

func New(capacity, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrInvalidConfig
	}
	w, err := window.New(capacity)
	if err != nil {
		return nil, err
	}
	m := &Matcher{window: w, limit: chainLimit, head: make(map[uint32]int), prev: make([]int, capacity)}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m, nil
}

func (m *Matcher) Capacity() int { return m.window.Capacity() }

func (m *Matcher) Probes() int { return m.probes }

func (m *Matcher) ResetProbes() { m.probes = 0 }

func (m *Matcher) Preset(data []byte) {
	m.window.AddBytes(data)
	for p := 0; p+3 <= len(data); p++ {
		m.link(hash(data[p:p+3]), p)
	}
	m.global = len(data)
}

func (m *Matcher) Insert(data []byte, index int) {
	m.window.Add(data[index])
	if index+3 <= len(data) {
		m.link(hash(data[index:index+3]), m.global)
	}
	m.global++
}

func (m *Matcher) Find(data []byte, start, maxLength int) Result {
	best := Result{}
	if start+3 > len(data) {
		return best
	}
	key := hash(data[start : start+3])
	candidate, ok := m.head[key]
	for n := 0; ok && n < m.limit; n++ {
		m.probes++
			distance := m.global - candidate
			if distance <= 0 || distance > m.window.Capacity() {
				break
			}
		limit := len(data) - start
		if maxLength < limit {
			limit = maxLength
		}
			length := 0
			for length < limit {
				position := candidate + length
				want := byte(0)
				if position < m.global {
					want = m.window.Byte(m.global - position)
				} else {
					want = data[start+position-m.global]
				}
				if data[start+length] != want {
					break
				}
				length++
			}
		if length > best.Length {
			best = Result{Distance: distance, Length: length}
		}
		older := m.prev[m.slot(candidate)]
			if older < 0 || older >= candidate {
				break
			}
			candidate = older
		}
	return best
}

func (m *Matcher) link(key uint32, global int) {
	slot := m.slot(global)
		if old, ok := m.head[key]; ok {
			m.prev[slot] = old
		} else {
			m.prev[slot] = -1
		}
	m.head[key] = global
}

func (m *Matcher) slot(global int) int { return global % m.window.Capacity() }

func hash(v []byte) uint32 {
	return uint32(v[0])<<16 ^ uint32(v[1])<<8 ^ uint32(v[2])
}
