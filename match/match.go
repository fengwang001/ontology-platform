// Package match finds longest matches in a window.Window with a bounded
// hash chain per position.
package match

import (
	"errors"

	"ontology/window"
	"ontology/wire"
)

// ErrBadConfig reports an illegal matcher configuration.
var ErrBadConfig = errors.New("match: window size and max chain must be positive")

// Matcher streams bytes through a window and answers match queries. Stream
// offsets count every appended byte; a preset dictionary occupies the first
// offsets. A match at pos may only reference offsets [0,pos).
type Matcher struct {
	win      *window.Window
	maxChain int
	head     []int32 // newest position per hash bucket, -1 when empty
	chain    []int32 // predecessor with same hash, indexed by pos mod cap
	cursor   int     // bytes appended so far
	inserted int     // count of positions present in chains
	probes   int64   // unexported counter: candidate positions examined
}

// New builds a matcher with the given window capacity and chain depth.
func New(windowSize, maxChain int) (*Matcher, error) {
	if windowSize <= 0 || maxChain <= 0 {
		return nil, ErrBadConfig
	}
	win, err := window.New(windowSize)
	if err != nil {
		return nil, err
	}
	head := make([]int32, 1<<16)
	for i := range head {
		head[i] = -1
	}
	return &Matcher{win: win, maxChain: maxChain, head: head,
		chain: make([]int32, windowSize)}, nil
}

// Window exposes the underlying window (used for preset dictionaries).
func (m *Matcher) Window() *window.Window { return m.win }

// Probes returns the total candidate positions examined since creation.
func (m *Matcher) Probes() int64 { return m.probes }

// Append stores p and builds chains for positions that now have 3 bytes.
func (m *Matcher) Append(p []byte) {
	for _, c := range p {
		m.win.Append([]byte{c})
		pos := m.cursor
		m.cursor++
		if pos+wire.MinMatch <= m.cursor {
			h := m.hashAt(pos)
			m.chain[pos%len(m.chain)] = m.head[h]
			m.head[h] = int32(pos)
			m.inserted = pos + 1
		}
	}
}

func (m *Matcher) byteAt(pos int) byte { return m.win.At(m.cursor - pos) }

func (m *Matcher) hashAt(pos int) uint32 {
	h := uint32(m.byteAt(pos)) | uint32(m.byteAt(pos+1))<<5 |
		uint32(m.byteAt(pos+2))<<11
	return h & uint32(len(m.head)-1)
}

// Find returns the longest match starting at stream offset pos, bounded by
// bytes already appended and by window capacity. (0,0) means no match.
func (m *Matcher) Find(pos int) (int, int) {
	if pos <= 0 || pos+wire.MinMatch > m.cursor {
		return 0, 0
	}
	maxLen := m.cursor - pos
	if maxLen > m.win.Cap() {
		maxLen = m.win.Cap()
	}
	h := m.hashAt(pos)
	bestD, bestL, seen := 0, 0, 0
	cand := m.head[h]
	for cand >= 0 && seen < m.maxChain {
		cp := int(cand)
		d := pos - cp
		if d <= 0 || d > m.win.Cap() {
			break
		}
		m.probes++
		seen++
		l := 0
		for l < maxLen && m.byteAt(cp+l) == m.byteAt(pos+l) {
			l++
		}
		if l > bestL {
			bestD, bestL = d, l
		}
		cand = m.chain[cp%len(m.chain)]
	}
	if bestL < wire.MinMatch {
		return 0, 0
	}
	return bestD, bestL
}
