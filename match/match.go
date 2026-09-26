// Package match finds longest matches in a sliding window via hash chains.
package match

import (
	"errors"

	"ontology/window"
)

var ErrZeroChain = errors.New("match: chain limit must be > 0")

const (
	minMatch = 3
	hashSize = 1 << 16
)

// Matcher owns a window (committed history plus the uncommitted pending tail)
// and hash chains over committed positions only.
type Matcher struct {
	win        *window.Window
	chainLimit int
	probes     int64
	committed  int // number of committed stream bytes
	tail       []byte
	head       []int
	prev       []int
}

func New(win *window.Window, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrZeroChain
	}
	return &Matcher{
		win:        win,
		chainLimit: chainLimit,
		head:       make([]int, hashSize),
		prev:       make([]int, hashSize),
	}, nil
}

func (m *Matcher) Probes() int64 { return m.probes }

// Committed reports how many bytes have been linked.
func (m *Matcher) Committed() int { return m.committed }

func hash3(a, b, c byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16
}

// linkOne links the triple ending at 1-based stream position pos.
func (m *Matcher) linkPos(pos int, a, b, c byte) {
	h := hash3(a, b, c) % hashSize
	m.prev[pos%hashSize] = m.head[h]
	m.head[h] = pos
}

// Append adds bytes to the window without committing them.
func (m *Matcher) Append(data []byte) { m.win.Push(data) }

// Commit links count bytes starting at committed offset base. The window
// already holds those bytes followed by the uncommitted pending suffix, so
// triples that straddle into pending are linked as well.
func (m *Matcher) Commit(base, count, winLen int) {
	for k := 0; k < count; k++ {
		idx := base + k
		if winLen-idx < 3 {
			continue // need idx and the two following bytes in the window
		}
		m.linkPos(idx+1,
			m.win.At(winLen-idx),
			m.win.At(winLen-idx-1),
			m.win.At(winLen-idx-2))
	}
	m.committed = base + count
}

// Look finds the longest match for pending[off:]; candidates come only from
// committed history, so a match can never reference a future pending byte.
func (m *Matcher) Look(pending []byte, off int) (length, dist int) {
	if off+minMatch > len(pending) {
		return 0, 0
	}
	cur := m.committed + off
	h := hash3(pending[off], pending[off+1], pending[off+2]) % hashSize
	cand := m.head[h]
	maxMatch := len(pending) - off
	for k := 0; k < m.chainLimit && cand != 0 && cand+hashSize > cur; k++ {
		m.probes++
		d := cur - (cand - 1)
		if d < 1 || d > m.win.Cap() || d > m.committed {
			break
		}
		l := 0
		for l < maxMatch && d+l <= m.win.Len() {
			if m.win.At(d+l) != pending[off+l] {
				break
			}
			l++
		}
		if l > length {
			length, dist = l, d
		}
		cand = m.prev[cand%hashSize]
	}
	return length, dist
}
