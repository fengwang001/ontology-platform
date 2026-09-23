// Package match finds longest LZ77 matches via a bounded hash chain.
// It depends only on package window.
package match

import (
	"errors"

	"ontology/window"
)

const MinMatch = 3

var ErrBadChain = errors.New("match: max chain depth must be > 0")

// Config configures a Matcher.
type Config struct {
	WindowCap int
	MaxChain  int
}

// Matcher is a 3-byte hash, single-chain matcher. Not concurrency-safe.
type Matcher struct {
	win      *window.Window
	head     []int // 16-bit hash -> newest absolute position, -1 if none
	next     []int // ring of predecessor positions
	mask     uint64
	maxChain int
	base     int   // absolute index of block[0]
	block    []byte
	inserted int   // absolute positions < inserted are in the chains
	examined int64 // unexported: candidate positions examined
}

// New constructs a Matcher from cfg.
func New(cfg Config) (*Matcher, error) {
	if cfg.MaxChain <= 0 {
		return nil, ErrBadChain
	}
	win, err := window.New(cfg.WindowCap)
	if err != nil {
		return nil, err
	}
	size := 1
	for size < win.Cap() {
		size <<= 1
	}
	m := &Matcher{win: win, head: make([]int, 1<<16), next: make([]int, size),
		mask: uint64(size - 1), maxChain: cfg.MaxChain}
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.next {
		m.next[i] = -1
	}
	return m, nil
}

func (m *Matcher) Window() *window.Window { return m.win }
func (m *Matcher) Examined() int64         { return m.examined }

func (m *Matcher) at(p int) byte {
	if p >= m.base {
		return m.block[p-m.base]
	}
	return m.win.AtAbs(p)
}

func (m *Matcher) hash(p int) uint32 {
	return (uint32(m.at(p))<<10 ^ uint32(m.at(p+1))<<5 ^ uint32(m.at(p+2))) & 0xffff
}

func (m *Matcher) insert(p, end int) {
	if p+2 >= end {
		return
	}
	h := m.hash(p)
	m.next[uint64(p)&m.mask] = m.head[h]
	m.head[h] = p
}

// Reset installs dict as a preset dictionary: its positions become
// chain candidates but no output is produced for them.
func (m *Matcher) Reset(dict []byte) {
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.next {
		m.next[i] = -1
	}
	m.win.AddAll(dict)
	end := m.win.Total()
	for p := 0; p+3 <= end; p++ {
		m.insert(p, end)
	}
	m.inserted = end
}

func (m *Matcher) find(pos, end int) (dist, n int) {
	if pos+MinMatch > end {
		return 0, 0
	}
	minPos := pos - m.win.Cap()
	bestLen := MinMatch - 1
	for cand, steps := m.head[m.hash(pos)], 0; cand >= minPos && steps < m.maxChain; steps++ {
		m.examined++
		if m.at(cand) == m.at(pos) && m.at(cand+bestLen) == m.at(pos+bestLen) {
			lim := end - pos
			if end-cand < lim {
				lim = end - cand
			}
			k := 0
			for k < lim && m.at(cand+k) == m.at(pos+k) {
				k++
			}
			if k > bestLen {
				bestLen, dist = k, pos-cand
			}
		}
	cand = m.next[uint64(cand)&m.mask]
	}
	if bestLen < MinMatch {
		return 0, 0
	}
	return dist, bestLen
}

// Record is one step: Len==1 is a literal, otherwise a back-reference.
type Record struct{ Dist, Len int; Lit byte }

// Process runs deterministic greedy compression; state carries across calls.
func (m *Matcher) Process(block []byte, out []Record) []Record {
	m.base, m.block = m.win.Total(), block
	start, end := m.base, m.base+len(block)
	for pos := start; pos < end; {
		for m.inserted < pos {
			m.insert(m.inserted, end)
			m.inserted++
		}
		if dist, n := m.find(pos, end); n >= MinMatch {
			out = append(out, Record{Dist: dist, Len: n})
			m.win.AddAll(block[pos-start : pos-start+n])
			pos += n
		} else {
			out = append(out, Record{Len: 1, Lit: block[pos-start]})
			m.win.Add(block[pos-start])
			pos++
		}
	}
	m.inserted = end
	return out
}
