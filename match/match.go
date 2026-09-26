// Package match finds longest matches with a capped hash-chain table.
package match

import (
	"errors"

	"ontology/window"
)

const MinMatch = 3

// MaxMatch bounds a single back-reference length.
const MaxMatch = 1 << 20

var ErrConfig = errors.New("match: chain limit must be positive")

// CandidateProbe is a found match; Len<MinMatch means no match.
type CandidateProbe struct {
	Dist  int
	Len   int
	Found bool
}

type Matcher struct {
	win        *window.Window
	head       []int
	prev       []int
	chain      int
	nextInsert int
	probes     uint64
	inited     bool
}

func New(win *window.Window, chainLimit int) *Matcher {
	if chainLimit <= 0 {
		panic(ErrConfig)
	}
	c := win.Capacity()
	return &Matcher{
		win:   win,
		head:  make([]int, 1<<16),
		prev:  make([]int, c),
		chain: chainLimit,
	}
}

func (m *Matcher) initHeads() {
	for i := range m.head {
		m.head[i] = -1
	}
}

func (m *Matcher) Pos() int { return m.nextInsert }

// Prefill inserts dictionary bytes that are not part of the current input.
func (m *Matcher) Prefill(p []byte) {
	if len(p) > m.win.Capacity() {
		p = p[len(p)-m.win.Capacity():]
	}
	m.win.Write(p)
}

// Commit advances the matcher over newly written bytes up to the window frontier.
func (m *Matcher) Commit(upto int) {
	if !m.inited {
		m.initHeads()
		m.inited = true
	}
	total := m.win.Len()
	for p := m.nextInsert; p < upto; p++ {
		if p+3 > total {
			break
		}
		h := m.hash(p)
		m.prev[p%len(m.prev)] = m.head[h]
		m.head[h] = p
		m.nextInsert = p + 1
	}
}

// Find searches at absolute position pos, bounded by end (exclusive).
func (m *Matcher) Find(pos, end int) CandidateProbe {
	if end-pos < MinMatch {
		return CandidateProbe{}
	}
	limit := end - pos
	if limit > MaxMatch {
		limit = MaxMatch
	}
	best := 0
	bestCand := -1
	cand := m.head[m.hash(pos)]
	for n := 0; n < m.chain && cand >= 0 && pos-cand <= m.win.Capacity(); n++ {
		m.probes++
		l := 0
		for l < limit && m.byteAt(cand+l) == m.byteAt(pos+l) {
			l++
		}
		if l > best {
			best = l
			bestCand = cand
			if l == limit {
				break
			}
		}
		cand = m.prev[cand%len(m.prev)]
	}
	if best < MinMatch {
		return CandidateProbe{}
	}
	return CandidateProbe{Dist: pos - bestCand, Len: best, Found: true}
}

func (m *Matcher) Probes() uint64 { return m.probes }

func (m *Matcher) byteAt(p int) byte {
	return m.win.At(m.win.Total() - p)
}

func (m *Matcher) hash(p int) uint32 {
	return uint32(m.byteAt(p)) | uint32(m.byteAt(p+1))<<8 | uint32(m.byteAt(p+2))<<16
}
