package match

import (
	"errors"

	"ontology/window"
)

const MinLength = 3

var (
	ErrZeroChainLimit = errors.New("chain limit must be positive")
	ErrLengthTooLong  = errors.New("match length exceeds 64-bit input")
)

type Matcher struct {
	win      *window.Window
	limit    int
	data     []byte
	base     uint64
	heads    map[uint32]uint64
	next     []uint64
	counters uint64
}

func New(win *window.Window, chainLimit int) (*Matcher, error) {
	if chainLimit <= 0 {
		return nil, ErrZeroChainLimit
	}
	return &Matcher{win: win, limit: chainLimit, heads: make(map[uint32]uint64)}, nil
}

func (m *Matcher) Reset(data []byte, base uint64) {
	m.data, m.base = data, base
	m.next = append(m.next[:0], make([]uint64, len(data))...)
	for i := range m.next {
		m.next[i] = ^uint64(0)
	}
	m.heads = make(map[uint32]uint64)
	m.counters = 0
	for i := 0; i+MinLength <= len(data); i++ {
		m.insert(uint64(i))
	}
}

func (m *Matcher) Candidates() uint64 { return m.counters }

func hash3(data []byte) uint32 {
	return uint32(data[0])<<16 ^ uint32(data[1])<<8 ^ uint32(data[2])
}

func (m *Matcher) insert(off uint64) {
	h := hash3(m.data[off:])
	if old, ok := m.heads[h]; ok {
		m.next[off] = old
	}
	m.heads[h] = off + m.base
}

func (m *Matcher) matchLength(pos, cand, limit uint64) int {
	distance := pos - cand
	for length := uint64(0); length < limit; length++ {
		source := pos + length - distance
		var b byte
		if source < m.base {
			var ok bool
			b, ok = m.win.At(pos + length + 1 - source)
			if !ok {
				return int(length)
			}
		} else {
			b = m.data[int(source-m.base)]
		}
		if b != m.data[int(pos+length-m.base)] {
			return false
		}
	}
	return int(limit)
}

func (m *Matcher) Match(pos uint64, maxLength uint64) (uint64, uint64) {
	if int(pos)+MinLength > len(m.data) || maxLength < MinLength {
		return 0, 0
	}
	h := hash3(m.data[pos:])
	cand, ok := m.heads[h]
	bestDist, bestLen := uint64(0), 0
	for n := 0; ok && n < m.limit; n++ {
		m.counters++
		dist := pos + m.base + 1 - cand
		if dist == 0 || dist > m.win.Size() || dist > pos+m.base {
			break
		}
		limit := maxLength
		if remain := uint64(len(m.data) - int(pos)); remain < limit {
			limit = remain
		}
		if length := m.matchLength(pos+m.base, cand, limit); length > bestLen {
			bestDist, bestLen = dist, length
		}
		idx := int(cand - m.base)
		if idx < 0 || idx >= len(m.next) || m.next[idx] == ^uint64(0) {
			break
		}
		cand = m.next[idx]
		ok = cand != ^uint64(0)
	}
	return bestDist, uint64(bestLen)
}
