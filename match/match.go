// Package match finds longest matches inside a sliding window via hash chains.
package match

import (
	"errors"

	"ontology/window"
)

// ErrBadConfig is returned for an illegal configuration.
var ErrBadConfig = errors.New("match: chain limit must be positive")

// MinMatch is the shortest match that is emitted.
const MinMatch = 3

// Matcher operates over one contiguous byte stream made of the window history
// plus the current lookahead block. Positions are absolute over that stream.
type Matcher struct {
	win    *window.Window
	head   []int // hash -> newest position with that hash (-1 when none)
	prev   []int // position -> previous position with the same hash
	chain  int
	maxLen int
	probes int64
	pos    int // next absolute position to insert
}

// New creates a Matcher. win provides history (already primed as a dictionary
// when non-empty); maxLen caps match length; chain caps candidates per query.
func New(win *window.Window, maxLen, chain int) (*Matcher, error) {
	if chain <= 0 {
		return nil, ErrBadConfig
	}
	const hbits = 16
	m := &Matcher{
		win:    win,
		head:   make([]int, 1<<hbits),
		prev:   make([]int, win.Cap()),
		chain:  chain,
		maxLen: maxLen,
		pos:    win.Len(),
	}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	// Seed chains from the already-present dictionary window.
	start := m.pos - win.Len()
	for p := start; p < m.pos; p++ {
		if b, ok := byteAt(win, p, MinMatch); ok {
			m.link(p, hash3(b))
		}
	}
	return m, nil
}

// Probes returns the total number of candidate positions examined.
func (m *Matcher) Probes() int64 { return m.probes }

func (m *Matcher) link(pos int, h uint32) {
	if idx := pos % m.win.Cap(); idx >= 0 {
		m.prev[idx] = m.head[h]
		m.head[h] = pos
	}
}

func hash3(b [3]byte) uint32 {
	return (uint32(b[0])<<10 ^ uint32(b[1])<<5 ^ uint32(b[2])) & 0xffff
}

func byteAt(w *window.Window, pos, n int) ([3]byte, bool) {
	var b [3]byte
	for i := 0; i < n; i++ {
		v, ok := w.At(pos + i)
		if !ok {
			return b, false
		}
		b[i] = v
	}
	return b, true
}

// Insert pushes one consumed byte into the window and, once three bytes are
// available, links its position into the hash chain. pos is absolute.
func (m *Matcher) Insert(pos int, b byte) {
	m.win.Push(b)
	if t, ok := byteAt(m.win, pos, MinMatch); ok {
		m.link(pos, hash3(t))
	}
}

// Find returns the longest match at absolute position pos. cur is the current
// segment starting at pos; bytes before it come from the dictionary window.
func (m *Matcher) Find(pos int, cur []byte) (distance, length int) {
	if len(cur) < MinMatch {
		return 0, 0
	}
	b := [3]byte{cur[0], cur[1], cur[2]}
	maxL := m.maxLen
	if maxL > len(cur) {
		maxL = len(cur)
	}
	limit := pos - m.win.Cap() // candidates older than this are out of window
	cand := m.head[hash3(b)]
	for steps := 0; cand > limit && steps < m.chain; steps++ {
		m.probes++
		l := MinMatch
		for l < maxL {
			cv, ok1 := m.get(cand+l, pos, cur)
			pv := cur[l]
			if !ok1 || cv != pv {
				break
			}
			l++
		}
		if l > length {
			distance, length = pos-cand, l
			if l == maxL {
				break
			}
		}
		cand = m.prev[cand%m.win.Cap()]
	}
	return distance, length
}

// get reads an absolute byte: >=base comes from cur, <base from the window.
func (m *Matcher) get(p, base int, cur []byte) (byte, bool) {
	if p >= base {
		if p-base >= len(cur) {
			return 0, false
		}
		return cur[p-base], true
	}
	return m.win.At(p)
}
