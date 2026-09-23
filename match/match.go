// Package match finds longest matches inside a sliding window using a
// bounded hash chain.
package match

import (
	"errors"

	"ontology/window"
)

// ErrConfig is returned for invalid matcher configuration.
var ErrConfig = errors.New("match: invalid configuration")

const (
	// MinMatch is the shortest match the encoder emits.
	MinMatch = 3
	// MaxMatch caps a single match length.
	MaxMatch = 258
	hashSize = 1 << 16
)

// Matcher works on one contiguous byte view that grows over time:
// data is logical history (index 0 = oldest retained byte), and
// windowLen is the number of trailing bytes usable as a dictionary
// (matches may point up to windowLen bytes back).
type Matcher struct {
	win        *window.Window
	maxChain   int
	head       []int // hash -> newest absolute index, -1 if none
	prev       []int // ring: previous candidate with same hash, -1 if none
	data       []byte
	inserted   int // number of leading positions already inserted
	windowLen  int // trailing prefix length that may serve as dictionary
	candidates int
}

// New builds a Matcher with the given window size and chain depth.
func New(winSize, maxChain int) (*Matcher, error) {
	if winSize <= 0 || maxChain <= 0 {
		return nil, ErrConfig
	}
	win := window.New(winSize)
	return &Matcher{
		win:      win,
		maxChain: maxChain,
		head:     make([]int, hashSize),
		prev:     make([]int, int(win.Cap())),
	}, nil
}

// Candidates reports how many candidate positions have been examined.
func (m *Matcher) Candidates() int { return m.candidates }

// WinCap reports the underlying window capacity.
func (m *Matcher) WinCap() int { return int(m.win.Cap()) }

// Seed installs a preset dictionary (previous block's tail). It must be
// called once, before the first Process.
func (m *Matcher) Seed(dict []byte) {
	m.windowLen = len(dict)
	m.data = append(m.data, dict...)
}

func hash3(b []byte) uint32 {
	return (uint32(b[0])<<10 ^ uint32(b[1])<<5 ^ uint32(b[2])) & (hashSize - 1)
}

// insertChain inserts positions [inserted, pos) and returns the previous
// candidate stored at the hash bucket for pos.
func (m *Matcher) insertChain(pos int) int {
	for m.inserted < pos {
		if m.inserted+MinMatch <= len(m.data) {
			h := hash3(m.data[m.inserted:])
			slot := m.inserted % cap(m.prev)
			m.prev[slot] = m.head[h]
			m.head[h] = m.inserted
		}
		m.inserted++
	}
	if pos+MinMatch > len(m.data) {
		return -1
	}
	h := hash3(m.data[pos:])
	return m.head[h]
}

// Find evaluates the position at the front of the newly appended segment.
// data is the full logical byte view (dictionary prefix followed by all
// bytes fed so far); pos is the position to match; futureStart is the
// first position not yet committed (match bytes must be < futureStart,
// except that a match starting at pos may extend into the new segment as
// long as it stays inside data). It returns match distance and length
// (0,0 if none of at least MinMatch).
func (m *Matcher) Find(data []byte, pos, futureStart int) (int, int) {
	m.data = data
	oldest := pos - int(m.win.Cap()) + 1
	if oldest < m.windowLen {
		oldest = m.windowLen // dictionary bytes cannot be overwritten logic
	}
	if oldest < 0 {
		oldest = 0
	}
	cand := m.insertChain(pos)
	bestLen, bestDist := 0, 0
	limit := pos + MaxMatch
	if limit > len(data) {
		limit = len(data)
	}
	avail := limit - pos
	for chain := 0; cand >= 0 && chain < m.maxChain; chain++ {
		m.candidates++
		if cand < oldest {
			break
		}
		if bestLen < avail && data[cand+bestLen] == data[pos+bestLen] &&
			data[cand] == data[pos] {
			n := 0
			for n < avail && data[cand+n] == data[pos+n] {
				n++
			}
			if n > bestLen {
				bestLen = n
				bestDist = pos - cand
				if n == avail {
					break
				}
			}
		}
		next := m.prev[cand%cap(m.prev)]
		if next >= cand { // ring slot overwritten: chain ends
			break
		}
		cand = next
	}
	if bestLen < MinMatch {
		return 0, 0
	}
	return bestDist, bestLen
}
