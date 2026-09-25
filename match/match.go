// Package match provides a bounded hash-chain longest-match finder over a
// sliding window.
package match

import "ontology/wire"
import "ontology/window"

const (
	MinMatch = 3
	hashSize = 1 << 12
)

// Source gives a byte at an absolute stream position. Positions inside the
// window and inside not-yet-committed pending data are both valid.
type Source interface {
	ByteAt(abs int) byte
}

// Matcher is a hash chain over committed window positions.
// A single Matcher is not safe for concurrent use.
type Matcher struct {
	win      *window.Window
	chainMax int
	head     []int // hash head (absolute position), -1 when empty
	prev     []int // ring slot pos%cap -> predecessor absolute position
	nextIdx  int   // next absolute position whose triple to index
	examined int64 // unexported count of inspected candidate positions
}

// New creates a matcher over win with a per-query candidate cap.
func New(win *window.Window, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, wire.ErrBadConfig
	}
	m := &Matcher{
		win:      win,
		chainMax: chainLimit,
		head:     make([]int, hashSize),
		prev:     make([]int, win.Cap()),
	}
	for i := range m.head {
		m.head[i] = -1
	}
	// Index triples already present in the window (prefill dictionary).
	m.nextIdx = win.Total() - win.Len()
	m.indexAvailable()
	return m, nil
}

// Examined returns the total number of candidate positions inspected.
func (m *Matcher) Examined() int64 { return m.examined }

// IndexNew indexes triples made available by bytes newly appended to the
// window since the previous call.
func (m *Matcher) IndexNew() { m.indexAvailable() }

func (m *Matcher) indexAvailable() {
	limit := m.win.Total() - MinMatch + 1
	for pos := m.nextIdx; pos < limit; pos++ {
		h := m.hashAt(m.win, pos)
		slot := pos % m.win.Cap()
		m.prev[slot] = m.head[h]
		m.head[h] = pos
	}
	m.nextIdx = limit
}

func (m *Matcher) hashAt(src Source, pos int) int {
	h := uint32(pos) ^ uint32(src.ByteAt(pos))<<5 ^ uint32(src.ByteAt(pos+1))<<13 ^
		uint32(src.ByteAt(pos+2))<<21
	return int(h&(hashSize-1)) ^ int(h>>16&(hashSize-1))
}

// Find returns the longest match for data starting at absolute position pos.
// avail is the first unavailable position; maxLen bounds the reported length.
// Distance 0 means no match of at least MinMatch was found.
func (m *Matcher) Find(pos int, src Source, avail, maxLen int) (distance, length int) {
	if avail-pos < MinMatch {
		return 0, 0
	}
	h := m.hashAt(src, pos)
	cand := m.head[h]
	best := 0
	for n := 0; n < m.chainMax && cand >= 0 && pos-cand <= m.win.Cap(); n++ {
		m.examined++
		l := 0
		limit := maxLen
		if r := avail - pos; r < limit {
			limit = r
		}
		for l < limit && src.ByteAt(cand+l) == src.ByteAt(pos+l) {
			l++
		}
		if l > best {
			best = l
			distance = pos - cand
		}
		if l == maxLen || best == maxLen {
			break
		}
		next := m.prev[cand%m.win.Cap()]
		if next < 0 || next >= cand || pos-next > m.win.Cap() {
			break
		}
		cand = next
	}
	if best < MinMatch {
		return 0, 0
	}
	return distance, best
}
