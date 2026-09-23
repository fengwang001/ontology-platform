package match

import (
	"errors"

	"ontology/window"
)

const (
	DefaultCapacity = 1 << 16
	DefaultMaxChain = 32
	bucketCount     = 1 << 16
	minMatch        = 3
)

var (
	ErrConfig     = errors.New("lz77: chain limit must be positive")
	ErrMatch      = errors.New("lz77: match shorter than minimum length")
	ErrNoCandidate = errors.New("lz77: no candidate")
)

type Config struct {
	WindowCapacity int
	MaxChain       int
}

type Matcher struct {
	win      *window.Window
	head     [bucketCount]int
	prev     []int
	maxChain int
	tail     [2]byte
	have     int
	candidates int
}

func New(c Config) (*Matcher, error) {
	if c.WindowCapacity == 0 {
		c.WindowCapacity = DefaultCapacity
	}
	if c.MaxChain <= 0 {
		return nil, ErrConfig
	}
	w, err := window.New(c.WindowCapacity)
	if err != nil {
		return nil, err
	}
	m := &Matcher{win: w, maxChain: c.MaxChain, prev: make([]int, c.WindowCapacity)}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m, nil
}

func (m *Matcher) WindowCapacity() int { return m.win.Capacity() }
func (m *Matcher) CandidateCount() int { return m.candidates }

func (m *Matcher) ResetCandidates() { m.candidates = 0 }

func (m *Matcher) Preset(data []byte) error {
	m.win.Preset(data)
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	m.have = 0
	for i := 2; i < len(data); i++ {
		h := hash3(data[i-2], data[i-1], data[i])
		pos := i - 2
		m.prev[pos%len(m.prev)] = m.head[h]
	m.head[h] = pos
	}
	if len(data) > 0 {
		m.have = min(2, len(data))
		copy(m.tail[:], data[len(data)-m.have:])
	}
	return nil
}

func (m *Matcher) Add(p []byte) {
	for _, b := range p {
		m.insert(b)
	}
}

func hash3(a, b, c byte) int {
	v := uint32(a)<<16 | uint32(b)<<8 | uint32(c)
	v ^= v >> 13
	v *= 2654435761
	return int(v & (bucketCount - 1))
}

func (m *Matcher) insert(b byte) {
	if m.have == 2 {
		start := m.win.Total() - 2
		h := hash3(m.tail[0], m.tail[1], b)
		m.prev[start%len(m.prev)] = m.head[h]
		m.head[h] = start
	} else {
		m.tail[m.have] = b
		m.have++
	}
	m.win.Add([]byte{b})
}

func (m *Matcher) byteAt(cur, n, pos int, data []byte) (byte, bool) {
	source := cur + n
	current := m.win.Total()
	if source < current {
		b, err := m.win.ByteAt(current - source)
		return b, err == nil
	}
	idx := pos + (source - current)
	if idx >= len(data) {
		return 0, false
	}
	return data[idx], true
}

func (m *Matcher) Find(data []byte, pos int) (distance, length int) {
	if pos+minMatch > len(data) || m.win.Len() < minMatch {
		return 0, 0
	}
	h := hash3(data[pos], data[pos+1], data[pos+2])
	cur := m.head[h]
	oldest := m.win.Total() - m.win.Len()
	limit := len(data) - pos
	for tried := 0; tried < m.maxChain && cur >= oldest && cur >= 0; tried++ {
		m.candidates++
		d := m.win.Total() - cur
		n := 0
		for n < limit {
			b, ok := m.byteAt(cur, n, pos, data)
			if !ok || b != data[pos+n] {
				break
			}
			n++
		}
		if n >= minMatch && n > length {
			distance, length = d, n
		}
		next := m.prev[cur%len(m.prev)]
		if next >= cur {
			break
		}
		cur = next
	}
	return distance, length
}
