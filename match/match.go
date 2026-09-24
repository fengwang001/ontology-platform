package match

import (
	"errors"

	"ontology/window"
)

var (
	ErrInvalidWindowCapacity = errors.New("lz77: window capacity must be positive")
	ErrInvalidChainLimit     = errors.New("lz77: chain limit must be positive")
)

type Matcher struct {
	window     *window.Window
	head       []int32
	next       []int32
	source     []byte
	base       int
	blockStart int
	limit      int
	probes     uint64
}

func New(windowCapacity, chainLimit int) (*Matcher, error) {
	win, err := window.New(windowCapacity)
	if err != nil {
		return nil, ErrInvalidWindowCapacity
	}
	if chainLimit <= 0 {
		return nil, ErrInvalidChainLimit
	}
	return &Matcher{window: win, head: make([]int32, 1<<16), next: make([]int32, 0), limit: chainLimit}, err
}

func (m *Matcher) Reset(dict []byte) {
	m.window.Reset()
	m.window.Write(dict)
	m.base = 0
	m.source = nil
	m.blockStart = 0
	m.next = m.next[:0]
	for i := range m.head {
		m.head[i] = -1
	}
	m.probes = 0
}

func (m *Matcher) SetBlock(data []byte) {
	kept := m.window.Len()
	if kept > len(m.source) {
		kept = len(m.source)
	}
	dict := m.source[len(m.source)-kept:]
	m.source = append(append(make([]byte, 0, kept+len(data)), dict...), data...)
	m.base = -kept
	m.blockStart = kept
	m.next = make([]int32, len(m.source))
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.source {
		m.next[i] = -1
		if i < m.blockStart {
			m.insert(i)
		}
	}
}

func (m *Matcher) Find(pos int) (distance, length int) {
	if pos < 0 || pos+3 > len(m.source)-m.blockStart {
		return 0, 0
	}
	absolute := m.blockStart + pos
	slot := m.hash(absolute)
	previous := m.head[slot]
	m.insert(absolute)
	best := 0
	examined := 0
	for candidate := int(previous); candidate >= 0 && examined < m.limit; candidate = int(m.next[candidate]) {
		if candidate > absolute || absolute-candidate > m.window.Capacity() {
			continue
		}
		examined++
		n := 3
		for absolute+n < len(m.source) && m.source[candidate+n] == m.source[absolute+n] {
			n++
		}
		if n > best {
			best, distance = n, absolute-candidate
		}
	}
	m.probes += uint64(examined)
	return distance, best
}

func (m *Matcher) Probes() uint64 { return m.probes }

func (m *Matcher) Insert(pos, count int) {
	for i := 0; i < count; i++ {
		m.insert(m.blockStart + pos + i)
	}
}

func (m *Matcher) insert(pos int) {
	if pos < 0 || pos+3 > len(m.source) {
		return
	}
	slot := m.hash(pos)
	m.next[pos] = m.head[slot]
	m.head[slot] = int32(pos)
}

func (m *Matcher) hash(pos int) int {
	return int(m.source[pos]) | int(m.source[pos+1])<<8 | int(m.source[pos+2])
}
