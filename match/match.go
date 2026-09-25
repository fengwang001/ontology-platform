// Package match finds longest back-reference matches inside a sliding
// window using hash chains with a bounded candidate chain length.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the minimum back-reference length (hash width).
const MinMatch = 4

const (
	headBits = 17
	headSize = 1 << headBits
	empty    = -1
)

// ErrBadChain rejects a zero or negative max chain length.
var ErrBadChain = errors.New("match: maxChain must be > 0")

// Matcher keeps hash chains over absolute input positions. Positions
// below the bound base resolve through the window; the rest via tail.
type Matcher struct {
	win      *window.Window
	maxChain int
	maxMatch int
	heads    [headSize]int64
	prev     []int64
	base     int64
	tail     []byte
	inserted int64
	examined int64
}

// New creates a matcher over win. If win is preloaded (preset
// dictionary), those positions become match candidates too.
func New(win *window.Window, maxChain, maxMatch int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrBadChain
	}
	if maxMatch < MinMatch {
		maxMatch = MinMatch
	}
	m := &Matcher{
		win: win, maxChain: maxChain, maxMatch: maxMatch,
		prev: make([]int64, win.Cap()), inserted: -int64(win.Len()),
	}
	for i := range m.heads {
		m.heads[i] = empty
	}
	return m, nil
}

// Examined returns the total number of candidates ever inspected.
func (m *Matcher) Examined() int64 { return m.examined }

// Bind sets the pending segment: tail[0] has absolute position base.
func (m *Matcher) Bind(tail []byte, base int64) { m.tail, m.base = tail, base }

func (m *Matcher) byteAt(pos int64) byte {
	if pos >= m.base {
		return m.tail[pos-m.base]
	}
	b, _ := m.win.At(m.base - pos)
	return b
}

func (m *Matcher) hash(pos int64) uint32 {
	v := uint32(m.byteAt(pos)) | uint32(m.byteAt(pos+1))<<8 |
		uint32(m.byteAt(pos+2))<<16 | uint32(m.byteAt(pos+3))<<24
	return (v * 2654435761) >> (32 - headBits)
}

func (m *Matcher) slot(pos int64) int64 {
	s := pos % int64(len(m.prev))
	if s < 0 {
		s += int64(len(m.prev))
	}
	return s
}

// Find returns the longest match at absolute position pos (dist, length);
// length is 0 when no candidate matches MinMatch bytes. Positions before
// pos are inserted into the hash chains as a side effect.
func (m *Matcher) Find(pos int64) (dist, length int) {
	end := m.base + int64(len(m.tail))
	limit := pos + int64(m.maxMatch)
	if limit > end {
		limit = end
	}
	for m.inserted < pos && m.inserted+MinMatch <= end {
		h := m.hash(m.inserted)
		m.prev[m.slot(m.inserted)] = m.heads[h]
		m.heads[h] = m.inserted
		m.inserted++
	}
	best, bestDist := 0, 0
	for cand, n := m.heads[m.hash(pos)], 0; cand != empty && n < m.maxChain; n++ {
		d := pos - cand
		if d > int64(m.win.Cap()) {
			break
		}
		m.examined++
		l := int64(0)
		for pos+l < limit && m.byteAt(cand+l) == m.byteAt(pos+l) {
			l++
		}
		if int(l) > best {
			best, bestDist = int(l), int(d)
			if pos+l >= limit {
				break // cannot be beaten
			}
		}
		if next := m.prev[m.slot(cand)]; next != empty && next < cand {
			cand = next
		} else {
			break
		}
	}
	return bestDist, best
}
