// Package match finds longest LZ77 matches inside a window via hash chains.
package match

import (
	"errors"

	"ontology/window"
)

// MinMatch is the shortest match worth emitting.
const MinMatch = 3

// ErrBadConfig is returned for invalid matcher configuration.
var ErrBadConfig = errors.New("match: maxChain must be positive")

// Matcher is a hash-chain matcher over a sliding window.
type Matcher struct {
	win      *window.Window
	maxChain int
	head     map[uint32]int // newest absolute position per hash
	prev     []int          // previous position in the same hash chain, ring indexed
	probes   int64          // unexported: candidate positions examined
}

// New builds a Matcher on a fresh window of the given capacity.
func New(cap, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrBadConfig
}
	win, err := window.New(cap)
	if err != nil {
		return nil, err
	}
	return &Matcher{
		win:      win,
		maxChain: maxChain,
		head:     make(map[uint32]int),
		prev:     make([]int, cap),
	}, nil
}

// FromWindow wraps an existing window (used for parallel preset dictionaries).
func FromWindow(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrBadConfig
	}
	m := &Matcher{
		win:      win,
		maxChain: maxChain,
		head:     make(map[uint32]int),
		prev:     make([]int, win.Cap()),
}
	m.index(win)
	return m, nil
}

func hash3(a, b, c byte) uint32 {
	return (uint32(a)<<16 ^ uint32(b)<<8 ^ uint32(c)) * 2654435761 >> 16
}

func (m *Matcher) index(win *window.Window) {
	base := win.Emitted() - win.Size()
	for i := 0; i+2 < win.Size(); i++ {
		pos := base + i
		h := hash3(win.At(pos), win.At(pos+1), win.At(pos+2))
		if old := m.head[h]; old != 0 {
			m.prev[pos%m.win.Cap()] = old
		}
		m.head[h] = pos + 1
	}
}

// Window exposes the underlying window.
func (m *Matcher) Window() *window.Window { return m.win }

// Probes returns the total number of candidate positions examined so far.
func (m *Matcher) Probes() int64 { return m.probes }

// Add feeds one newly emitted byte at absolute position win.Emitted().
func (m *Matcher) Add(c byte) {
	pos := m.win.Emitted()
	if pos >= 2 {
		h := hash3(m.win.At(pos-2), m.win.At(pos-1), c)
		if old := m.head[h]; old != 0 {
			m.prev[pos%m.win.Cap()] = old
		}
		m.head[h] = pos - 1
	}
	m.win.Put(c)
}

// Find returns the longest match (distance, length) starting at the byte
// about to be added; limit bytes after the window end are available in lookahead.
// Length 0 means no usable match.
func (m *Matcher) Find(a, b, c byte, lookahead []byte) (int, int) {
	h := hash3(a, b, c)
	pos := m.win.Emitted()
	need := append([]byte{a, b, c}, lookahead...)
	bestLen := 0
	bestDist := 0
	candP1 := m.head[h]
	chain := 0
	for candP1 != 0 && chain < m.maxChain {
		m.probes++
		cand := candP1 - 1
		dist := pos - cand
		if dist > m.win.Cap() {
			break
		}
		l := 0
		for ; l < len(need); l++ {
			var got byte
			if cand+l < pos {
				got = m.win.At(cand + l)
			} else {
				got = need[cand+l-pos]
			}
			if got != need[l] {
				break
			}
		}
		if l > bestLen {
			bestLen, bestDist = l, dist
		}
		if l == len(need) {
			break
		}
		candP1 = m.prev[cand%m.win.Cap()]
		chain++
	}
	return bestDist, bestLen
}
