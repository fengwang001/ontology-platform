// Package match finds longest backref matches inside a sliding window
// using hash chains with a bounded candidate chain length.
package match

import "ontology/window"

const hashBits = 16

// Matcher searches a window for matches of the current block.
// Positions are absolute: block[0] sits at position base, everything
// before base lives in the window (history or preset dictionary).
type Matcher struct {
	win   *window.Window
	chain int
	head  []int // hash -> newest indexed position+1 (0 = none)
	prev  []int // ring by position: position -> previous same-hash position+1
	block []byte
	base  int
	cand  int64 // total candidate positions examined (unexported counter)
}

// New returns a Matcher over win examining at most chainLimit
// candidates per position. chainLimit must be > 0.
func New(win *window.Window, chainLimit int) *Matcher {
	if chainLimit <= 0 {
		panic("match: chain limit must be positive")
	}
	return &Matcher{
		win:   win,
		chain: chainLimit,
		head:  make([]int, 1<<hashBits),
		prev:  make([]int, win.Cap()),
	}
}

// Candidates reports how many candidate positions have been examined.
func (m *Matcher) Candidates() int64 { return m.cand }

// BeginBlock starts matching over block, whose first byte sits at the
// window's current length.
func (m *Matcher) BeginBlock(block []byte) {
	m.block = block
	m.base = m.win.Len()
}

func (m *Matcher) at(pos int) byte {
	if pos >= m.base {
		return m.block[pos-m.base]
	}
	return m.win.At(pos)
}

func (m *Matcher) hash(pos int) int {
	v := uint32(m.at(pos)) | uint32(m.at(pos+1))<<8 |
		uint32(m.at(pos+2))<<16 | uint32(m.at(pos+3))<<24
	return int(v * 2654435761 >> (32 - hashBits))
}

// Index adds position pos to the hash chains. It is a no-op near the
// block end where a 4-byte hash no longer fits.
func (m *Matcher) Index(pos int) {
	if pos+4 > m.base+len(m.block) {
		return
	}
	h := m.hash(pos)
	m.prev[pos%m.win.Cap()] = m.head[h]
	m.head[h] = pos + 1
}

// Longest returns the distance and length of the longest match for the
// bytes starting at absolute position pos, considering only matches
// with distance <= window capacity and length <= maxLen.
func (m *Matcher) Longest(pos, maxLen int) (dist, ln int) {
	if maxLen < 4 || pos+4 > m.base+len(m.block) {
		return 0, 0
	}
	cap := m.win.Cap()
	oldest := pos - cap
	c := m.head[m.hash(pos)] - 1
	for steps := 0; c >= 0 && c >= oldest && steps < m.chain; steps++ {
		m.cand++
		l := 0
		for l < maxLen && m.at(c+l) == m.at(pos+l) {
			l++
		}
		if l > ln {
			ln, dist = l, pos-c
			if ln >= maxLen {
				break
			}
		}
		c = m.prev[c%cap] - 1
	}
	return dist, ln
}
