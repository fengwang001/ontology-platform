package match

import "ontology/internal/window"

const MinLength = 3

type Matcher struct {
	win      *window.Ring
	head     []int32
	next     []int32
	maxChain int
	data     []byte
	base     int
	probes   int
}

func New(windowCap, maxChain int) *Matcher {
	if windowCap <= 0 || maxChain <= 0 {
		panic("match: invalid configuration")
	}
	return &Matcher{
		win:      window.New(windowCap),
		head:     make([]int32, 1<<16),
		next:     make([]int32, windowCap),
		maxChain: maxChain,
	}
}

func (m *Matcher) Reset() {
	m.win.Reset()
	for i := range m.head {
		m.head[i] = -1
	}
	m.data, m.base, m.probes = nil, 0, 0
}

func (m *Matcher) Probes() int { return m.probes }
func (m *Matcher) Window() int { return m.win.Cap() }

func (m *Matcher) Preset(dict []byte) {
	m.Reset()
	tail := dict
	if len(tail) > m.win.Cap() {
		tail = tail[len(tail)-m.win.Cap():]
	}
	off := len(dict) - len(tail)
	m.win.SeedTail(tail, len(dict))
	m.data = dict
	m.base = off
	m.insertPositions(off, len(dict)-MinLength+1)
	m.data = nil
	m.base = len(dict)
}

func (m *Matcher) Load(data []byte) {
	m.data = data
	m.base = m.win.Total()
}

func (m *Matcher) Find(index int) (distance, length int) {
	if index < 0 || index+MinLength > len(m.data) {
		return 0, 0
	}
	pos := m.base + index
	key := hash(m.data[index : index+MinLength])
	cand := int(m.head[key])
	limit := pos - m.win.Cap()
	if limit < 0 {
		limit = 0
	}
	best := 0
	for tries := 0; tries < m.maxChain && cand >= limit && cand < pos; tries++ {
		m.probes++
		n := m.common(cand, pos, len(m.data)-index)
		if n >= MinLength && n >= best {
			best, distance = n, pos-cand
		}
		next := int(m.next[cand%m.win.Cap()])
		if next < 0 || next >= cand {
			break
		}
		cand = next
	}
	return distance, best
}

func (m *Matcher) Advance(n int) {
	if n <= 0 {
		return
	}
	start, end := m.base, m.base+n
	for _, b := range m.data[:n] {
		m.win.AddByte(b)
	}
	m.insertPositions(start, end-MinLength+1)
	m.base = end
	m.data = m.data[n:]
}

func (m *Matcher) insertPositions(start, end int) {
	if end > m.win.Total()-MinLength+1 {
		end = m.win.Total() - MinLength + 1
	}
	for ; start < end; start++ {
		key := hashAt(m, start)
		idx := start % m.win.Cap()
		m.next[idx] = m.head[key]
		m.head[key] = int32(start)
	}
}

func (m *Matcher) common(candidate, current, maxLen int) int {
	n := 0
	for n < maxLen && m.byteAt(candidate+n) == m.byteAt(current+n) {
		n++
	}
	return n
}

func (m *Matcher) byteAt(absolute int) byte {
	if absolute < m.base {
		return m.win.ByteAt(absolute)
	}
	return m.data[absolute-m.base]
}

func hash(b []byte) uint32 {
	return (uint32(b[0])<<10 ^ uint32(b[1])<<5 ^ uint32(b[2])) & 0xffff
}

func hashAt(m *Matcher, p int) uint32 {
	return (uint32(m.byteAt(p))<<10 ^
		uint32(m.byteAt(p+1))<<5 ^
		uint32(m.byteAt(p+2))) & 0xffff
}
