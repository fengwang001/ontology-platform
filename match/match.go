// Package match is a hash-chain longest-match finder over a sliding
// window. Candidate chains are bounded by a configurable limit.
// Depends only on package window. Not goroutine-safe.
package match

import (
	"encoding/binary"
	"errors"

	"ontology/window"
)

// MinMatch is the shortest match worth emitting: it equals the hash
// width, and a back-reference record costs at least 3 bytes.
const MinMatch = 4

// ErrConfig reports a non-positive window capacity or chain limit.
var ErrConfig = errors.New("match: window capacity and chain limit must be positive")

// Matcher finds longest matches within the last windowCap bytes.
type Matcher struct {
	win      *window.Window
	maxChain int
	head     map[uint32]int // 4-byte hash -> newest absolute position+1
	prev     []int          // prev[pos%cap] -> previous position+1, 0 = none
	roll     uint32         // last 4 inserted bytes, big-endian
	n        int            // total bytes inserted

	candidates int // total candidate positions ever examined
}

// New returns a Matcher with the given window capacity and the
// maximum number of chain candidates examined per Find call.
func New(windowCap, maxChain int) (*Matcher, error) {
	if windowCap <= 0 || maxChain <= 0 {
		return nil, ErrConfig
	}
	w, err := window.New(windowCap)
	if err != nil {
		return nil, err
	}
	return &Matcher{win: w, maxChain: maxChain, head: map[uint32]int{}, prev: make([]int, windowCap)}, nil
}

// Candidates returns how many candidate positions Find has examined.
func (m *Matcher) Candidates() int { return m.candidates }

// Insert adds data to the history: every 4-byte position becomes a
// future match candidate. Positions are absolute and grow monotonically.
func (m *Matcher) Insert(data []byte) {
	for _, b := range data {
		m.win.Add(b)
		m.roll = m.roll<<8 | uint32(b)
		if m.n >= 3 { // position m.n-3 now has 4 bytes
			p := m.n - 3
			m.prev[p%len(m.prev)] = m.head[m.roll]
			m.head[m.roll] = p + 1
		}
		m.n++
	}
}

// Find returns the distance and length of the longest match for data
// within the window, capped at maxLen. It returns (0, 0) when no
// match of at least MinMatch bytes exists.
func (m *Matcher) Find(data []byte, maxLen int) (dist, length int) {
	if len(data) < MinMatch || maxLen < MinMatch {
		return 0, 0
	}
	if maxLen > len(data) {
		maxLen = len(data)
	}
	best, bestDist := 0, 0
	pos := m.n // absolute position of data[0]
	for v, chain := m.head[binary.BigEndian.Uint32(data[:MinMatch])], 0; v > 0 && chain < m.maxChain; chain++ {
		p := v - 1
		if d := pos - p; d <= m.win.Cap() {
			m.candidates++
			if l := m.matchLen(data, d, maxLen); l > best {
				best, bestDist = l, d
				if best >= maxLen {
					break
				}
			}
			v = m.prev[p%len(m.prev)]
		} else {
			break // older candidates are even farther away
		}
	}
	if best < MinMatch {
		return 0, 0
	}
	return bestDist, best
}

// matchLen compares data against the bytes at distance d: the first d
// bytes come from the window, beyond that the match overlaps data
// itself (dist < length self-reference).
func (m *Matcher) matchLen(data []byte, d, maxLen int) int {
	l := 0
	for l < maxLen {
		var b byte
		if l < d {
			b = m.win.At(d - l)
		} else {
			b = data[l-d]
		}
		if data[l] != b {
			break
		}
		l++
	}
	return l
}
