// Package match finds longest LZ77 matches with a bounded-length hash chain.
// It depends only on ontology/window (through the caller's view of history).
//
// A Matcher is not safe for concurrent use.
package match

import "errors"

const (
	// MinLength is the shortest match that can be emitted as a back-reference.
	MinLength = 3

	hashBits = 17
	hashSize = 1 << hashBits
	hashMask = hashSize - 1
)

// ErrMaxChain is returned when a non-positive chain limit is requested.
var ErrMaxChain = errors.New("match: max chain must be positive")

// Matcher maintains a hash chain over absolute byte positions.
// For each 3-byte hash it remembers the newest position; per-position links
// form the chain and live in a ring the size of the history window.
type Matcher struct {
	windowCap int64
	maxChain  int
	head      []int64 // hash -> newest position+1 (0 means empty)
	prev      []int64 // ring slot (pos mod windowCap) -> previous pos+1

	// checked is the total number of candidate positions ever examined.
	checked int64
}

// New creates a Matcher for a window of windowCap with at most maxChain
// candidates examined per Find call.
func New(windowCap, maxChain int) (*Matcher, error) {
	if windowCap <= 0 {
		return nil, errors.New("match: window capacity must be positive")
	}
	if maxChain <= 0 {
		return nil, ErrMaxChain
	}
	return &Matcher{
		windowCap: int64(windowCap),
		maxChain:  maxChain,
		head:      make([]int64, hashSize),
		prev:      make([]int64, windowCap),
	}, nil
}

// Checked returns the total number of candidate positions examined so far.
func (m *Matcher) Checked() int64 { return m.checked }

func hash3(b0, b1, b2 byte) uint64 {
	v := uint64(b0)<<16 | uint64(b1)<<8 | uint64(b2)
	return (v * 0x9E3779B97F4A7C15) >> (64 - hashBits) & hashMask
}

// Insert records that the 3 bytes b0,b1,b2 begin at absolute position pos.
func (m *Matcher) Insert(pos int64, b0, b1, b2 byte) {
	h := hash3(b0, b1, b2)
	m.prev[pos%m.windowCap] = m.head[h]
	m.head[h] = pos + 1
}

// Find returns (distance, length) of the longest match for the data starting
// at absolute position pos, considering at most maxLen bytes. get(abs) must
// return the byte at any absolute position in [pos-windowCap, pos+maxLen).
// A zero result means no match of at least MinLength exists.
func (m *Matcher) Find(pos int64, maxLen int, get func(abs int64) byte) (dist, length int) {
	if maxLen < MinLength {
		return 0, 0
	}
	h := hash3(get(pos), get(pos+1), get(pos+2))
	oldest := pos - m.windowCap
	bestLen := 0
	bestPos := int64(0)
	c := m.head[h] - 1
	for n := 0; n < m.maxChain && c >= 0 && c >= oldest; n++ {
		m.checked++
		l := 0
		for l < maxLen && get(c+int64(l)) == get(pos+int64(l)) {
			l++
		}
		if l > bestLen {
			bestLen, bestPos = l, c
		}
		c = m.prev[c%m.windowCap] - 1
	}
	if bestLen < MinLength {
		return 0, 0
	}
	return int(pos - bestPos), bestLen
}
