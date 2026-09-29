// Package match finds longest LZ77 matches with a bounded-length hash chain.
package match

import (
	"fmt"

	"ontology/window"
)

// Matcher chains absolute positions sharing a 3-byte hash. Pending (not yet
// committed) bytes are never in the window; Find compares candidates against
// both window history and the caller-supplied pending prefix.
type Matcher struct {
	win   *window.Window
	head  map[uint32]int64
	chain []int64 // indexed by absolute position mod window capacity
	maxC  int
	maxL  int
	exam  int64 // unexported: total candidate positions examined
}

func New(win *window.Window, maxChain, maxLen int) *Matcher {
	if maxChain <= 0 {
		panic(fmt.Sprintf("match: maxChain must be > 0, got %d", maxChain))
	}
	if maxLen < 3 {
		panic(fmt.Sprintf("match: maxLen must be >= 3, got %d", maxLen))
	}
	return &Matcher{
		win:   win,
		head:  make(map[uint32]int64),
		chain: fillMinus1(win.Cap()),
		maxC:  maxChain,
		maxL:  maxLen,
	}
}

func fillMinus1(n int) []int64 {
	s := make([]int64, n)
	for i := range s {
		s[i] = -1
	}
	return s
}

// Examined returns total candidate positions examined since construction.
func (m *Matcher) Examined() int64 { return m.exam }

func hash3(b0, b1, b2 byte) uint32 {
	return uint32(b0)<<10 ^ uint32(b1)<<5 ^ uint32(b2)
}

func (m *Matcher) byteAt(pos, base int64, pend []byte) (byte, bool) {
	if pos >= base {
		j := int(pos - base)
		if j >= len(pend) {
			return 0, false
		}
		return pend[j], true
	}
	d := int(base - pos)
	if d > m.win.Len() {
		return 0, false
	}
	return m.win.At(d), true
}

// Find searches a match for pend[0] (absolute position base). limit caps the
// returned length. Returns (0,0) when no match of length >= 3 exists.
func (m *Matcher) Find(base int64, pend []byte, limit int) (int, int) {
	if len(pend) < 3 || m.win.Len() < 3 || limit < 3 {
		return 0, 0
	}
	if limit > m.maxL {
		limit = m.maxL
	}
	if limit > len(pend) {
		limit = len(pend)
	}
	cand, ok := m.head[hash3(pend[0], pend[1], pend[2])]
	if !ok {
		return 0, 0
	}
	cp := int64(m.win.Cap())
	low := base - int64(m.win.Len())
	bestD, bestL := 0, 0
	for k := 0; k < m.maxC; k++ {
		if cand < low {
			break
		}
		m.exam++
		d := int(base - cand)
		l := 3
		for l < limit {
			cb, ok1 := m.byteAt(cand+int64(l), base, pend)
			if !ok1 || cb != pend[l] {
				break
			}
			l++
		}
		if l > bestL {
			bestL, bestD = l, d
			if l >= limit {
				break
			}
		}
		next := m.chain[cand%cp]
		if next < 0 || next >= cand {
			break // absent or stale slot reused by a later position
		}
	cand = next
	}
	return bestD, bestL
}

// Commit appends p to the window and inserts every 3-byte head position.
func (m *Matcher) Commit(p []byte) {
	cp := int64(m.win.Cap())
	for i := 0; i+2 < len(p); i++ {
		pos := m.win.Pos() + int64(i)
		h := hash3(p[i], p[i+1], p[i+2])
		if old, ok := m.head[h]; ok {
			m.chain[pos%cp] = old
		} else {
			m.chain[pos%cp] = -1
		}
		m.head[h] = pos
	}
	m.win.Write(p)
}
