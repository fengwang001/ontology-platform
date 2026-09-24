// Package match finds the longest LZ77 match using a bounded hash chain over
// a window. It depends only on package window.
package match

import (
	"errors"

	"ontology/window"
)

// ErrBadConfig is returned when the chain depth is not positive.
var ErrBadConfig = errors.New("match: chain depth must be positive")

const hashSlots = 1 << 16

// Matcher is a hash-chain match finder. A single instance is not safe for
// concurrent use; parallel block compression uses one instance per block.
type Matcher struct {
	w       *window.Window
	chain   int // max candidates examined per position
	head    []int
	prev    []int // prev[abs pos mod cap] = previous chain position
	probes  int64 // non-exported count of examined candidates
	inserted int  // next absolute position to insert
}

// New creates a Matcher over w, visiting at most chainDepth candidates.
func New(w *window.Window, chainDepth int) (*Matcher, error) {
	if chainDepth <= 0 {
		return nil, ErrBadConfig
	}
	return &Matcher{
		w:     w,
		chain: chainDepth,
		head:  make([]int, hashSlots),
		prev:  make([]int, w.Cap()),
	}, nil
}

// Probes reports the total number of candidate positions ever examined.
func (m *Matcher) Probes() int64 { return m.probes }

func hash3(b0, b1, b2 byte) int {
	return (int(b0)<<10 ^ int(b1)<<5 ^ int(b2)) & (hashSlots - 1)
}

// insert registers positions up to pos-1 so Find at pos only sees history.
func (m *Matcher) insert(pos int) {
	for m.inserted < pos {
		p := m.inserted
		if p+2 < m.w.Total() {
			h := hash3(m.w.Abs(p), m.w.Abs(p+1), m.w.Abs(p+2))
			m.prev[p%len(m.prev)] = m.head[h]
			m.head[h] = p
		}
		m.inserted++
	}
}

// Find returns the best match starting at absolute position pos: distance back
// (0 if none) and length >= 3. maxLen bounds extension into available bytes.
func (m *Matcher) Find(pos, maxLen int) (dist, length int) {
	m.insert(pos)
	if maxLen < 3 || pos+2 >= m.w.Total() {
		return 0, 0
	}
	h := hash3(m.w.Abs(pos), m.w.Abs(pos+1), m.w.Abs(pos+2))
	cand := m.head[h]
	oldest := m.w.Start()
	for depth := 0; depth < m.chain && cand >= oldest && cand < pos; depth++ {
		m.probes++
		limit := maxLen
		if room := m.w.Total() - cand; room < limit {
			limit = room
		}
		if l := m.w.PrefixLen(pos, cand, limit); l > length {
			length = l
			dist = pos - cand
			if length == maxLen {
				break
			}
		}
		next := m.prev[cand%len(m.prev)]
		if next >= cand { // stale slot overwritten by a newer position
			break
		}
		cand = next
	}
	return dist, length
}

// Preset seeds the matcher with a dictionary already held in the window:
// dictionary bytes occupy the first dictLen absolute positions.
func (m *Matcher) Preset(dictLen int) { m.insert(dictLen) }
