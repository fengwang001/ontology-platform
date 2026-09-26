// Package match finds the longest match in a sliding window via hash chains.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest match worth emitting instead of literals.
const MinMatch = 3

// ErrChainLimit is returned for a non-positive chain limit.
var ErrChainLimit = errors.New("match: chain limit must be positive")

// Matcher keeps hash chains over at most Cap recent inserted bytes.
type Matcher struct {
	win    *window.Window
	limit  int
	head   map[uint32]int64
	prev   []int64
	n      int64
	probes int64
}

// New builds a matcher with positive window capacity and chain limit.
func New(windowCap, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrChainLimit
	}
	win, err := window.New(windowCap)
	if err != nil {
		return nil, err
	}
	m := &Matcher{
		win:   win,
		limit: chainLimit,
		head:  make(map[uint32]int64),
		prev:  make([]int64, windowCap),
	}
	for i := range m.prev {
		m.prev[i] = -1
	}
	return m, nil
}

// Reset clears all state.
func (m *Matcher) Reset() {
	m.win.Reset()
	clear(m.head)
	for i := range m.prev {
		m.prev[i] = -1
	}
	m.n, m.probes = 0, 0
}

// Probes reports total candidate positions examined (unexported counter rule).
func (m *Matcher) Probes() int64 { return m.probes }

// Cap reports the window capacity.
func (m *Matcher) Cap() int { return m.win.Cap() }

func hash3(a, b, c byte) uint32 {
	return uint32(a)<<16 ^ uint32(b)<<8 ^ uint32(c)
}

// Insert appends one historical byte and links its ending triplet's chain.
func (m *Matcher) Insert(b byte) {
	m.win.Add(b)
	m.n++
	if m.n < MinMatch {
		return
	}
	start := m.n - MinMatch
	h := hash3(m.win.At(3), m.win.At(2), m.win.At(1))
	slot := int(start % int64(m.win.Cap()))
	if old, ok := m.head[h]; ok {
		m.prev[slot] = old
	}
	m.head[h] = start
}

// Find returns the best (distance, length) for the lookahead against inserted
// history. Overlapping matches (length > distance) are supported.
func (m *Matcher) Find(input []byte) (dist, length int) {
	maxLen := len(input)
	if m.n < MinMatch || maxLen < MinMatch {
	return 0, 0
	}
	h := hash3(input[0], input[1], input[2])
	q, ok := m.head[h]
	if !ok {
		return 0, 0
	}
	capv := int64(m.win.Cap())
	for step := 0; step < m.limit; step++ {
		d := m.n - q
		if d < 1 || d > capv {
			break
		}
		m.probes++
		l := 0
		for l < maxLen {
			var src byte
			if int64(l) < d {
				src = m.win.At(int(d - int64(l)))
			} else {
				src = input[l-int(d)]
			}
			if src != input[l] {
				break
			}
			l++
		}
		if l > length {
			dist, length = int(d), l
			if l == maxLen {
				break
			}
		}
		next := m.prev[int(q%capv)]
		if next < 0 || next >= q {
			break
		}
	q = next
	}
	return dist, length
}

// Seed loads a preset dictionary as ordinary historical bytes.
func (m *Matcher) Seed(p []byte) {
	for _, b := range p {
		m.Insert(b)
	}
}
