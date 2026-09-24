// Package match finds longest matches in a sliding window via hash chains.
package match

import (
	"errors"

	"ontology/window"
)

const (
	hashBits = 16
	hashSize = 1 << hashBits
	// MinMatch is the shortest match worth encoding.
	MinMatch = 3
	// MaxMatch caps a single back-reference length.
	MaxMatch = 1 << 15
)

// ErrBadConfig rejects a zero chain limit.
var ErrBadConfig = errors.New("match: maxChain must be positive")

// Matcher holds hash chains over the bytes already published to its window.
type Matcher struct {
	win      *window.Window
	head     []int32 // hash bucket -> slot, -1 when empty
	prev     []int32 // slot -> previous chain slot
	stamp    []int64 // slot -> absolute position currently stored there
	pos      int64   // absolute count of published bytes
	maxChain int
	// candidates counts every candidate position examined (non-exported probe).
	candidates int64
}

// New builds a Matcher over win with the per-position chain walk limit.
func New(win *window.Window, maxChain int) (*Matcher, error) {
	if maxChain <= 0 {
		return nil, ErrBadConfig
	}
	cap := win.Cap()
	m := &Matcher{
		win:      win,
		head:     make([]int32, hashSize),
		prev:     make([]int32, cap),
		stamp:    make([]int64, cap),
		maxChain: maxChain,
	}
	for i := range m.head {
		m.head[i] = -1
	}
	return m, nil
}

// Candidates returns the total candidate positions examined so far.
func (m *Matcher) Candidates() int64 { return m.candidates }

func hash3(b0, b1, b2 byte) uint32 {
	return (uint32(b0)<<10 ^ uint32(b1)<<5 ^ uint32(b2)) & (hashSize - 1)
}

// Seed inserts hash chains for a preset dictionary (oldest first).
func (m *Matcher) Seed(dict []byte) {
	for i := 0; i+MinMatch <= len(dict); i++ {
		m.insert(int64(i), dict[i], dict[i+1], dict[i+2])
	}
	m.pos = int64(len(dict))
}

func (m *Matcher) insert(p int64, b0, b1, b2 byte) {
	slot := int(p % int64(cap(m.prev)))
	h := hash3(b0, b1, b2)
	m.prev[slot] = m.head[h]
	m.head[h] = int32(slot)
	m.stamp[slot] = p
}

// byteAt resolves a position relative to match start: j < d lives in the
// window, later bytes come from the not-yet-published lookahead.
func (m *Matcher) byteAt(d int, j int, ahead []byte) byte {
	if j < d {
		return m.win.At(d - j)
}
	return ahead[j-d]
}

// Find returns the longest match (>= MinMatch) for ahead[0:], limited by
// ahead, MaxMatch and the reference distance; length 0 means no match.
func (m *Matcher) Find(ahead []byte) (length, distance int) {
	if len(ahead) < MinMatch {
		return 0, 0
	}
	h := hash3(ahead[0], ahead[1], ahead[2])
	oldest := m.pos - int64(m.win.Cap())
	slot := m.head[h]
	limit := m.maxChain
	for k := 0; k < limit && slot >= 0; k++ {
		p := m.stamp[int(slot)]
		if p < oldest || p >= m.pos {
			break
		}
		m.candidates++
		d := int(m.pos - p)
		maxL := len(ahead) + d
		if maxL > MaxMatch {
			maxL = MaxMatch
		}
		l := 0
		for l < maxL && m.byteAt(d, l, ahead) == ahead[l] {
			l++
		}
		if l >= MinMatch && l > length {
			length, distance = l, d
			if l == MaxMatch {
				break
			}
		}
		slot = m.prev[int(slot)]
	}
	return length, distance
}

// Advance publishes n positions after the matching window was extended with
// those n bytes; tail supplies up to 2 following lookahead bytes for hashing.
func (m *Matcher) Advance(n int, tail []byte) {
	end := m.pos + int64(n)
	get := func(q int64) byte {
		if q < end {
			return m.win.At(int(end - q))
		}
		return tail[int(q-end)]
	}
	for i := 0; i < n; i++ {
		p := m.pos + int64(i)
		if p+2 < end+int64(len(tail)) {
			m.insert(p, get(p), get(p+1), get(p+2))
		}
	}
	m.pos = end
}
