// Package match finds longest LZ77 matches with a bounded hash chain over a
// window.Window. Each input position examines at most ChainLimit candidates.
package match

import (
	"errors"

	"ontology/window"
)

const (
	MinMatch     = 3
	hashBits     = 16
	hashSize     = 1 << hashBits
	hashMask     = hashSize - 1
	noPos        = -1
)

var ErrConfig = errors.New("match: chain limit must be positive")

type Matcher struct {
	w            *window.Window
	chain        int
	head         []int // hash -> newest absolute position
	prev         []int // ring indexed by position % cap -> previous position
	candExamined int64
}

func New(w *window.Window, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrConfig
}
	return &Matcher{
		w:     w,
		chain: chainLimit,
		head:  make([]int, hashSize),
		prev:  make([]int, w.Cap()),
	}, nil
}

func (m *Matcher) resetHeads() {
	for i := range m.head {
		m.head[i] = noPos
	}
}

// CandExamined is the total number of chain candidates ever inspected.
func (m *Matcher) CandExamined() int64 { return m.candExamined }

func hash3(b []byte) int {
	return (int(b[0])<<16 ^ int(b[1])<<8 ^ int(b[2])) & hashMask
}

// Insert puts one absolute position into the chain.
func (m *Matcher) Insert(p int) {
	s := m.w.Slice(p, MinMatch)
	if len(s) < MinMatch {
		return
	}
	h := hash3(s)
	m.prev[uint(p)%uint(m.w.Cap())] = m.head[h]
	m.head[h] = p
}

// Preset inserts every MinMatch-able position of the already-populated window
// below basePos. Used to seed a block with the previous block's tail.
func (m *Matcher) Preset(basePos int) {
	m.resetHeads()
	start := m.w.Base()
	if start < basePos-m.w.Cap() {
		start = basePos - m.w.Cap()
	}
	for p := start; p+MinMatch <= m.w.End(); p++ {
		m.Insert(p)
	}
}

func (m *Matcher) commonLen(a, b, limit int) int {
	n := 0
	for n < limit && m.w.At(a+n) == m.w.At(b+n) {
		n++
	}
	return n
}

// Find returns the best match for target position t (distance, length).
// length 0 means no candidate matched MinMatch bytes. Only bytes strictly
// before end are compared, so a match ending at end-1 may still extend.
func (m *Matcher) Find(t, end int) (dist, length int) {
	s := m.w.Slice(t, MinMatch)
	if len(s) < MinMatch {
		return 0, 0
	}
	h := hash3(s)
	limit := end - t
	cand := m.head[h]
	slot := uint(t) % uint(m.w.Cap())
	for i := 0; i < m.chain; i++ {
		if cand == noPos || cand <= t-limit || !m.w.Has(cand) {
			break
		}
		m.candExamined++
		if n := m.commonLen(cand, t, limit); n >= MinMatch && n > length {
			dist, length = t-cand, n
		}
		next := m.prev[uint(cand)%uint(m.w.Cap())]
		if next >= cand { // slot reused by a newer/foreign position
			break
		}
		cand = next
	}
	m.prev[slot] = m.head[h]
	m.head[h] = t
	return dist, length
}

// Extend grows an existing match when new bytes arrive; it does not touch the
// chains. Returns the new length.
func (m *Matcher) Extend(t, end, dist, length int) int {
	src := t - dist
	for t+length < end && m.w.At(src+length) == m.w.At(t+length) {
		length++
	}
	return length
}
