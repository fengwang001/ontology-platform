// Package match implements a hash-chain longest-match finder over a window.
package match

import (
	"errors"

	"ontology/window"
)

const (
	// MinLen is the shortest match emitted.
	MinLen = 3
	// MaxLen caps match length (bounded lookahead, see DESIGN.md).
	MaxLen = 258
	hashN  = 1 << 16
)

// ErrChain is returned when the chain limit is invalid.
var ErrChain = errors.New("match: chain limit must be > 0")

// Matcher stores hash chains over bytes committed to its window. Find may
// also compare against uncommitted "future" bytes supplied at lookup time.
type Matcher struct {
	win      *window.Window
	head     [hashN]int32 // newest absolute position per bucket; -1 empty
	prev     []int32      // chain ring indexed by pos % cap+? (see slots)
	slots    int          // len(prev) == window cap + 1? see constructor
	chainMax int
	total    int64 // absolute number of bytes ever inserted
	examined int64 // non-exported candidate-examination counter
}

// New builds a Matcher over a fresh window with the given capacity and the
// maximum number of chain candidates examined per Find.
func New(capacity, chainMax int) (*Matcher, error) {
	if chainMax <= 0 {
		return nil, ErrChain
	}
	w, err := window.New(capacity)
	if err != nil {
		return nil, err
	}
	m := &Matcher{win: w, chainMax: chainMax, slots: capacity}
	m.prev = make([]int32, capacity)
	for i := range m.head {
		m.head[i] = -1
	}
	return m, nil
}

// Window exposes the underlying window (shared with the encoder).
func (m *Matcher) Window() *window.Window { return m.win }

// Examined returns the total number of candidate positions inspected.
func (m *Matcher) Examined() int64 { return m.examined }

func hash3(b []byte) uint32 {
	return (uint32(b[0])<<10 ^ uint32(b[1])<<5 ^ uint32(b[2])) & (hashN - 1)
}

// byteAt reads a byte at absolute position pos: committed bytes come from the
// window, positions at/after base from the uncommitted src slice.
func (m *Matcher) byteAt(pos int64, base int64, src []byte, j int) byte {
	if k := int(pos - base); k >= 0 && k < len(src) {
		return src[k]
	}
	return m.win.At(int(base - pos))
}

// insert commits one byte at absolute position total, chaining its hash.
func (m *Matcher) insert(b0, b1, b2 byte, pos int64) {
	h := (uint32(b0)<<10 ^ uint32(b1)<<5 ^ uint32(b2)) & (hashN - 1)
	slot := int(pos) % m.slots
	if m.total-pos < int64(m.slots) {
		m.prev[slot] = m.head[h]
	}
	m.head[h] = int32(pos)
}

// Add commits p (already appended to the window by the caller) into chains.
func (m *Matcher) Add(p []byte) {
	base := m.total
	for j := 0; j+2 < len(p); j++ {
		m.insert(p[j], p[j+1], p[j+2], base+int64(j))
	}
	m.total += int64(len(p))
}

// Find returns the best match for src[0:], where src begins immediately after
// the committed window. Only the first src[limit] bytes may participate.
// It returns distance (1-based from src start) and length (0 if < MinLen).
func (m *Matcher) Find(src []byte, limit int) (dist, length int) {
	if limit < MinLen || len(src) < MinLen {
		return 0, 0
	}
	if limit > len(src) {
		limit = len(src)
	}
	base := m.total // absolute position of src[0]
	h := hash3(src)
	cand := m.head[h]
	best := 0
	for n := 0; n < m.chainMax && cand >= 0; n++ {
		m.examined++
		cp := int64(cand)
		if base-cp > int64(m.win.Cap()) {
			break
		}
		l := 0
		for l < limit {
			var cb byte
			if k := int(cp - base); k < 0 {
				cb = m.win.At(int(base + int64(l) - cp))
			} else {
				cb = src[k+l]
			}
			if cb != src[l] {
				break
			}
			l++
		}
		if l > best {
			best = l
			dist = int(base - cp)
		}
		if best == limit {
			break
		}
		slot := int(cand) % m.slots
		next := m.prev[slot]
		if next >= cand {
			break // slot overwritten: chain no longer reliable
		}
		cand = next
	}
	if best < MinLen {
		return 0, 0
	}
	if best > MaxLen {
		best = MaxLen
	}
	return dist, best
}
