// Package match implements a hash-chain longest-match finder over a window.
package match

import (
	"errors"

	"ontology/window"
	"ontology/wire"
)

// ErrInvalidConfig is returned for non-positive capacities or chain limits.
var ErrInvalidConfig = errors.New("match: capacity and chainLimit must be positive")

// Matcher is a greedy hash-chain LZ77 matcher over one contiguous input.
type Matcher struct {
	win      *window.Window
	cap      int
	maxChain int
	head     []int32
	next     []int32
	probes   int64
	abs      int
}

// New creates a Matcher holding up to capacity bytes as back-reference history.
func New(capacity, maxChain int) (*Matcher, error) {
	if capacity <= 0 || maxChain <= 0 {
		return nil, ErrInvalidConfig
	}
	win, err := window.New(capacity)
	if err != nil {
		return nil, err
	}
	return &Matcher{
		win:      win,
		cap:      capacity,
		maxChain: maxChain,
		head:     make([]int32, 1<<16),
		next:     make([]int32, capacity),
	}, nil
}

// Preset loads dictionary bytes that may be matched but are not emitted.
func (m *Matcher) Preset(data []byte) {
	for _, c := range data {
		m.addNode(c)
	}
}

// ProbeCount reports how many candidate positions have ever been examined.
func (m *Matcher) ProbeCount() int64 { return m.probes }

func hash3(a, b, c byte) uint32 {
	return (uint32(a)<<10 ^ uint32(b)<<5 ^ uint32(c)) & 0xffff
}

// addNode records the 3-byte sequence ending in the newly written byte.
func (m *Matcher) addNode(c byte) {
	if m.abs >= 2 {
		h := hash3(m.win.At(2), m.win.At(1), c)
		idx := m.abs % m.cap
		m.next[idx] = m.head[h]
		m.head[h] = int32(idx)
	}
	m.win.WriteByte(c)
	m.abs++
}

// Encode runs greedy LZ77 over block, appending wire records to out. Preset and
// earlier committed history are addressable but are never re-emitted.
func (m *Matcher) Encode(block, out []byte) []byte {
	data := block
	i := 0
	litStart := 0
	emit := func(end int) {
		if end > litStart {
			out = wire.AppendLiteral(out, data[litStart:end])
		}
	}
	for i+wire.MinMatch <= len(data) {
		h := hash3(data[i], data[i+1], data[i+2])
		bestDist, bestLen := 0, wire.MinMatch-1
		cand := int(m.head[h])
		for k := 0; k < m.maxChain && cand >= 0; k++ {
			m.probes++
			vabs := cand - m.abs%m.cap
			if vabs < 0 {
				vabs += m.cap
			}
			dist := m.abs + i - vabs
			if dist <= 0 || dist > m.cap {
				break
			}
			maxL := len(data) - i
			l := 0
			for l < maxL {
				var cb byte
				if l < i {
					cb = data[i+l-dist]
				} else {
					cb = m.win.At(dist - (l - i))
				}
				if cb != data[i+l] {
					break
				}
				l++
			}
			if l > bestLen {
				bestLen, bestDist = l, dist
			}
			if bestLen == maxL {
				break
			}
			next := int(m.next[cand])
			if next == cand {
				break
			}
			cand = next
		}
		if bestDist > 0 {
			emit(i)
			out = wire.AppendRef(out, bestDist, bestLen)
			for j := 0; j < bestLen; j++ {
				m.addNode(data[i+j])
			}
			i += bestLen
			litStart = i
			continue
		}
		m.addNode(data[i])
		i++
	}
	emit(len(data))
	return out
}
