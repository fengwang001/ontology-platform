// Package match finds longest LZ77 matches over a window using hash chains.
package match

import "ontology/window"

const (
	hashBits = 16
	hashSize = 1 << hashBits
	hashMask = hashSize - 1
	MinMatch = 3
	MaxMatch = 1 << 28
	nilPos   = int64(-1)
)

// Mapper maps the match package to its window dependency for tests.
type Matcher struct {
	win      *window.Window
	chainMax int
	head     []int64 // head[h] = newest position with hash h
	prev     []int64 // ring: predecessor in chain for each absolute position
	capWin   int
	examined int64 // total candidate positions examined (unexported)
}

// New creates a matcher over win with a per-lookup candidate cap.
func New(win *window.Window, chainMax int) *Matcher {
	capWin := win.Capacity()
	m := &Matcher{
		win:      win,
		chainMax: chainMax,
		head:     make([]int64, hashSize),
		prev:     make([]int64, capWin),
		capWin:   capWin,
	}
	for i := range m.head {
		m.head[i] = nilPos
	}
	return m
}

// Examined reports the cumulative number of candidate positions examined.
func (m *Matcher) Examined() int64 { return m.examined }

func hash3(b0, b1, b2 byte) uint32 {
	return (uint32(b0) | uint32(b1)<<8 | uint32(b2)<<16) & hashMask
}

// Insert indexes position p (the byte at p must be live in the window).
func (m *Matcher) Insert(p int64) {
	b0, ok0 := m.win.ByteAtPos(p)
	b1, ok1 := m.win.ByteAtPos(p + 1)
	b2, ok2 := m.win.ByteAtPos(p + 2)
	if !ok0 || !ok1 || !ok2 {
		return
	}
	h := hash3(b0, b1, b2)
	m.prev[p%int64(m.capWin)] = m.head[h]
	m.head[h] = p
}

// Find searches the chain for the longest match of data (the pending bytes
// starting at absolute position cur). Returns distance (0 if none) and length.
func (m *Matcher) Find(cur int64, data []byte) (dist, length int) {
	if len(data) < MinMatch {
		return 0, 0
	}
	h := hash3(data[0], data[1], data[2])
	cand := m.head[h]
	best := MinMatch - 1
	limit := cur - int64(m.capWin)
	for n := 0; n < m.chainMax && cand != nilPos && cand >= 0 && cand > limit; n++ {
		m.examined++
		l := m.compare(cur, cand, int(cur-cand), data)
		if l > best {
			best = l
			dist = int(cur - cand)
			if l == len(data) {
				break
			}
		}
		next := m.prev[cand%int64(m.capWin)]
		if next >= cand { // stale / wrapped entry
			break
		}
		cand = next
	}
	if best < MinMatch {
		return 0, 0
	}
	if best > MaxMatch {
		best = MaxMatch
	}
	return dist, best
}

func (m *Matcher) compare(cur, cand int64, dist int, data []byte) int {
	n := 0
	max := len(data)
	if max > MaxMatch {
		max = MaxMatch
	}
	for n < max {
		cp := cand + int64(n)
		var cb byte
		if cp < cur {
			b, ok := m.win.ByteAtPos(cp)
			if !ok {
				break
			}
			cb = b
		} else {
			cb = data[n-dist] // overlap: read already-expanded pending byte
		}
		if cb != data[n] {
			break
		}
		n++
	}
	return n
}
