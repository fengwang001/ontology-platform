// Package match finds longest matches with a bounded hash chain over a window.
package match

import "ontology/window"

const hashSize = 1 << 16

// Matcher is a hash-chain LZ matcher. Positions are absolute stream offsets;
// only the latest cap bytes are reachable through the window.
type Matcher struct {
	win     *window.Window
	chain   int
	pos     uint64 // absolute position of the next byte to insert
	head    []int64
	prev    []int64
	lookups int // total candidate positions examined
}

// New builds a matcher. cap is the window capacity, maxChain the candidate cap.
func New(cap, maxChain int) *Matcher {
	if cap <= 0 || maxChain <= 0 {
		panic("match: cap and maxChain must be positive")
	}
	return &Matcher{
		win:   window.New(cap),
		chain: maxChain,
		head:  make([]int64, hashSize),
		prev:  make([]int64, cap),
	}
}

// Win exposes the underlying history window.
func (m *Matcher) Win() *window.Window { return m.win }

// Lookups returns the total number of candidate positions examined so far.
func (m *Matcher) Lookups() int { return m.lookups }

func hash3(b0, b1, b2 byte) uint32 {
	return (uint32(b0)<<10 ^ uint32(b1)<<5 ^ uint32(b2)) & (hashSize - 1)
}

// Insert registers one byte triple (b0,b1,b2) at the current absolute position
// and pushes b0 into the history window.
func (m *Matcher) Insert(b0, b1, b2 byte) {
	p := m.pos
	h := hash3(b0, b1, b2)
	hp := int(h)
	c := int(p % uint64(len(m.prev)))
	if old := m.head[hp]; old >= 0 && uint64(old) < p-uint64(m.win.Cap()) {
		old = -1
	}
	m.prev[c] = old
	m.head[hp] = int64(p)
	m.win.Put(b0)
	m.pos++
}

// Preset registers a prefix dictionary (oldest first), registering triples and
// filling the window. Bytes outside the capacity age out normally.
func (m *Matcher) Preset(data []byte) {
	for i := 0; i < len(data); i++ {
		var b1, b2 byte
		if i+1 < len(data) {
			b1 = data[i+1]
		}
		if i+2 < len(data) {
			b2 = data[i+2]
		}
		m.Insert(data[i], b1, b2)
	}
}

// Find examines candidates whose registered triple equals block[i:i+3], then
// inserts that triple. The match may extend inside block, including overlap
// references (distance < length). It returns the best distance and length.
func (m *Matcher) Find(i int, block []byte) (dist, length int) {
	b0, b1, b2 := block[i], block[i+1], block[i+2]
	h := hash3(b0, b1, b2)
	cand := m.head[h]
	best := 0
	for n := 0; n < m.chain && cand >= 0; n++ {
		cp := uint64(cand)
		m.lookups++
	if m.pos-cp > uint64(m.win.Cap()) {
		break
	}
	d := int(m.pos - cp)
	l := 0
	maxL := len(block) - i
	for l < maxL {
		var src byte
		if l < d {
			v, ok := m.win.ByteAt(d - l)
			if !ok {
				break
			}
			src = v
		} else {
			src = block[i+l-d]
		}
		if block[i+l] != src {
			break
		}
		l++
	}
	if l > best {
		best, dist = l, d
	}
	cand = m.prev[cp%uint64(len(m.prev))]
	if uint64(cand) >= cp {
		break // stale: chain slot was reused
	}
}
	m.Insert(b0, b1, b2)
	return dist, best
}
