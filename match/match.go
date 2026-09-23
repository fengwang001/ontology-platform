// Package match finds the longest history match with a bounded hash chain.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest back reference worth emitting.
const MinMatch = 3

// MaxMatch caps a single reference length; long runs use several references.
const MaxMatch = 1 << 16

// ErrConfig reports an invalid matcher configuration.
var ErrConfig = errors.New("match: max chain must be > 0")

// Matcher keeps one head per 3-byte hash and a linked chain of prior trigger
// positions. Positions are absolute: a preset dictionary occupies negative
// offsets, so emitted distances always count back from the current offset.
// It is not safe for concurrent use.
type Matcher struct {
	win     *window.Window
	head    map[uint32]int
	prev    map[int]int
	chain   int
	total   int
	pending []byte
}

// New builds a matcher over a fresh window of capacity winCap; maxChain bounds
// candidates inspected per Find call.
func New(winCap, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrConfig
	}
	w, err := window.New(winCap)
	if err != nil {
		return nil, err
	}
	return &Matcher{win: w, head: map[uint32]int{}, prev: map[int]int{}, chain: maxChain}, nil
}

// Preset seeds history from dict (the previous block tail in parallel mode).
// It must be called before Find or Advance.
func (m *Matcher) Preset(dict []byte) {
	m.win.Write(dict)
	base := -len(dict)
	for i := 0; i+2 < len(dict); i++ {
		m.insert(base+i, hash3(dict[i:]))
	}
}

func hash3(p []byte) uint32 {
	return uint32(p[0])<<11 ^ uint32(p[1])<<5 ^ uint32(p[2])
}

func (m *Matcher) insert(pos int, h uint32) {
	if old, ok := m.head[h]; ok {
		m.prev[pos] = old
	}
	m.head[h] = pos
}

// Find returns the longest match starting at pending[k]. ahead is the number of
// bytes from k that may be covered (available input), and the returned length
// never exceeds ahead or MaxMatch.
func (m *Matcher) Find(k, ahead int) (dist, length int) {
	p := m.pending
	if k+MinMatch > len(p) {
		return 0, 0
	}
	limit := ahead
	if limit > MaxMatch {
		limit = MaxMatch
	}
	cand, ok := m.head[hash3(p[k:])]
	bestDist, bestLen := 0, MinMatch-1
	for n := 0; n < m.chain && ok; n++ {
		d := k - cand
		m.total++
		if d <= 0 {
			break
		}
		if d > m.win.Capacity() {
			cand, ok = m.prev[cand]
			continue
		}
		l := 0
		for l < limit {
			if l >= d || l >= m.win.Capacity() || l >= m.win.Len() {
				break
			}
			if m.win.At(d-l) != p[k+l] {
				break
			}
			l++
		}
		if l > bestLen {
			bestDist, bestLen = d, l
			if l == limit {
				break
			}
		}
		cand, ok = m.prev[cand]
	}
	if bestLen < MinMatch {
		return 0, 0
	}
	return bestDist, bestLen
}

// Advance commits the first n pending bytes into history and chains triggers.
func (m *Matcher) Advance(n int) {
	p := m.pending[:n]
	base := m.win.Len()
	for i := 0; i+2 < len(p); i++ {
		m.insert(base+i, hash3(p[i:]))
	}
	m.win.Write(p)
	m.pending = m.pending[n:]
}

// SetPending replaces the uncommitted view; the encoder owns the bytes.
func (m *Matcher) SetPending(p []byte) { m.pending = p }

// Pending returns the still-uncommitted bytes after an Advance.
func (m *Matcher) Pending() []byte { return m.pending }

// Candidates reports total candidate probes since construction.
func (m *Matcher) Candidates() int { return m.total }

// WinCap exposes the logical window capacity.
func (m *Matcher) WinCap() int { return m.win.Capacity() }
