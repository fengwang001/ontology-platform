// Package match finds longest LZ77 matches with a bounded hash chain.
package match

import "ontology/window"

const MinLength = 3

const hashBits = 16

// Matcher maintains hash chains over the bytes held in a Window.
// It is not safe for concurrent use.
type Matcher struct {
	win      *window.Window
	head     []int // hash -> most recent absolute position, -1 if none
	prev     []int // ring, previous candidate for a position
	chainMax int
	probes   int
}

func New(win *window.Window, chainMax int) *Matcher {
	if chainMax <= 0 {
		panic("match: chainMax must be positive")
	}
	m := &Matcher{
		win:      win,
		head:     make([]int, 1<<hashBits),
		prev:     make([]int, win.Cap()),
		chainMax: chainMax,
	}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m
}

func (m *Matcher) hash(p []byte) uint32 {
	return (uint32(p[0])<<10 ^ uint32(p[1])<<5 ^ uint32(p[2])) & (1<<hashBits - 1)
}

func (m *Matcher) Insert(pos int) {
	if !m.win.Live(pos + 2) {
		return
	}
	h := m.hash([]byte{
		m.win.ByteAt(pos),
		m.win.ByteAt(pos + 1),
		m.win.ByteAt(pos + 2),
	})
	c := m.head[h]
	m.head[h] = pos
	m.prev[pos%m.win.Cap()] = c
}

// Find returns the best match ending strictly before pos.
// length is 0 when no match of at least MinLength exists; otherwise
// distance is pos-candidate (>=1). probes is the number of candidates tested.
func (m *Matcher) Find(pos int, maxLen int) (length, distance, probes int) {
	if pos+MinLength-1 > m.win.Base()+m.win.Len()-1 {
		return 0, 0, 0
	}
	h := m.hash([]byte{
		m.win.ByteAt(pos),
		m.win.ByteAt(pos + 1),
		m.win.ByteAt(pos + 2),
	})
	cand := m.head[h]
	best, bestDist := 0, 0
	limit := m.win.Base()
	for k := 0; k < m.chainMax && cand >= limit; k++ {
		m.probes++
		probes++
		if cand+maxLen <= pos {
			cand = m.prev[cand%m.win.Cap()]
			continue
		}
		l := 0
		for l < maxLen && cand+l < pos && m.win.ByteAt(cand+l) == m.win.ByteAt(pos+l) {
			l++
		}
		if l > best {
			best, bestDist = l, pos-cand
			if l == maxLen {
				break
			}
		}
		cand = m.prev[cand%m.win.Cap()]
	}
	if best < MinLength {
		return 0, 0, probes
	}
	return best, bestDist, probes
}

// Probes returns the total candidate probes performed since construction.
func (m *Matcher) Probes() int { return m.probes }

func (m *Matcher) addProbes(n int) { m.probes += n }
