// Package span records monotone output↔original offset correspondences.
package span

import "sort"

// Seg is a run [O,O+OLen) in output mapped to [I,I+ILen) in original.
// Zero OLen means deleted original bytes; zero ILen means inserted bytes.
// Segments must be appended in increasing, gap-free order on both axes.
type Seg struct {
	O    int
	I    int
	OLen int
	ILen int
}

// Map is an ordered, coalesced list of segments.
type Map struct {
	segs []Seg
	// checks counts segments inspected by the most recent query (must be O(log n)).
	checks int
}

// Add appends a segment, coalescing with the previous when lengths match.
func (m *Map) Add(o, i, olen, ilen int) {
	if n := len(m.segs); n > 0 {
		p := &m.segs[n-1]
		if p.O+p.OLen == o && p.I+p.ILen == i &&
			((p.OLen > 0 && p.ILen > 0 && olen > 0 && ilen > 0) ||
				(p.ILen == 0 && ilen == 0)) {
			p.OLen += olen
			p.ILen += ilen
			return
		}
	}
	m.segs = append(m.segs, Seg{o, i, olen, ilen})
}

// Rebuild creates a Map from already-coalesced segments (used by stitching).
func Rebuild(segs []Seg) *Map { return &Map{segs: append([]Seg(nil), segs...)} }

// Truncate drops output beyond o, clipping the segment that crosses o.
func (m *Map) Truncate(o int) {
	segs := m.segs
	for len(segs) > 0 && segs[len(segs)-1].O >= o {
		segs = segs[:len(segs)-1]
	}
	if len(segs) > 0 {
		p := &segs[len(segs)-1]
		if end := p.O + p.OLen; end > o {
			keep := o - p.O
			p.OLen = keep
			if p.ILen > 0 {
				p.ILen = keep
			}
		}
	}
	m.segs = segs
}

// Segs returns the underlying segments for stitching (caller copies if needed).
func (m *Map) Segs() []Seg { return m.segs }

// LenOut and LenOrig report covered endpoints.
func (m *Map) LenOut() int {
	if len(m.segs) == 0 {
		return 0
	}
	p := m.segs[len(m.segs)-1]
	return p.O + p.OLen
}

// LenOrig reports the covered original endpoint.
func (m *Map) LenOrig() int {
	if len(m.segs) == 0 {
		return 0
	}
	p := m.segs[len(m.segs)-1]
	return p.I + p.ILen
}

// Checks returns segments inspected by the last ToOrig/ToOut call.
func (m *Map) Checks() int { return m.checks }

// ToOrig maps output offset o (0..LenOut) to an original offset.
func (m *Map) ToOrig(o int) int {
	m.checks = 0
	k := m.search(func(s Seg) bool { m.checks++; return s.O+s.OLen > o })
	if k == len(m.segs) {
		return m.LenOrig()
	}
	s := m.segs[k]
	if o <= s.O {
		return s.I // inserted boundary: start of this run
	}
	return s.I + (o - s.O)
}

// ToOut maps original offset i (0..LenOrig) to an output offset.
func (m *Map) ToOut(i int) int {
	m.checks = 0
	k := m.search(func(s Seg) bool { m.checks++; return s.I+s.ILen > i })
	if k == len(m.segs) {
		return m.LenOut()
	}
	s := m.segs[k]
	if i <= s.I || s.OLen == 0 {
		return s.O // deleted run: fold onto the following output point
	}
	return s.O + (i - s.I)
}

func (m *Map) search(gt func(Seg) bool) int {
	return sort.Search(len(m.segs), func(k int) bool { return gt(m.segs[k]) })
}
