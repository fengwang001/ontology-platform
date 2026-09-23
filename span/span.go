// Package span records the correspondence between original byte ranges and
// normalized output ranges and answers bidirectional offset queries.
package span

// Segment maps half-open original range [O0,O1) onto output range [P0,P1).
// Kept content has equal lengths; a deleted range has P0==P1; a synthetic
// inserted byte has O0==O1.
type Segment struct {
	O0, O1 int // original [start,end)
	P0, P1 int // output [start,end)
}

// Map is an immutable, sorted set of segments covering both coordinate
// spaces contiguously.
type Map struct {
	seg []Segment
	// probes counts intervals inspected by the most recent query.
	probes int
}

// Builder accumulates segments, merging adjacent ones whenever the merge
// preserves the mapping (both identity runs, or both pure-deletion runs).
type Builder struct {
	seg []Segment
}

func mergeable(a, b Segment) bool {
	aDel, bDel := a.P1 == a.P0, b.P1 == b.P0
	aKeep, bKeep := a.O1-a.O0 == a.P1-a.P0, b.O1-b.O0 == b.P1-b.P0
	aIns, bIns := a.O1 == a.O0, b.O1 == b.O0
	return !aIns && !bIns && ((aDel && bDel) || (aKeep && bKeep))
}

// Add appends a segment. Segments must be added in coordinate order.
func (b *Builder) Add(s Segment) {
	if n := len(b.seg); n > 0 {
		last := &b.seg[n-1]
		if last.O1 == s.O0 && last.P1 == s.P0 && mergeable(*last, s) {
			last.O1 = s.O1
			last.P1 = s.P1
			return
		}
	}
	b.seg = append(b.seg, s)
}

// Build freezes the accumulated segments into a Map.
func (b *Builder) Build() *Map { return &Map{seg: b.seg} }

// Len returns the number of mapping intervals.
func (m *Map) Len() int { return len(m.seg) }

// find returns the index of the last segment whose start key is <= x,
// preferring an exact-start segment at ties, and counts inspected intervals.
func (m *Map) find(key func(Segment) int, x int) int {
	m.probes = 0
	lo, hi := 0, len(m.seg)
	for lo < hi {
		m.probes++
		mid := (lo + hi) / 2
		if key(m.seg[mid]) <= x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	i := lo - 1
	if i+1 < len(m.seg) && key(m.seg[i+1]) == x {
		i++
	}
	return i
}

// Probes reports intervals inspected by the latest ToOrig/ToOut query.
func (m *Map) Probes() int { return m.probes }

// ToOrig maps an output offset to an original offset.
func (m *Map) ToOrig(o int) int {
	i := m.find(func(s Segment) int { return s.P0 }, o)
	if i < 0 {
		return m.seg[0].O0
	}
	s := m.seg[i]
	if o >= s.P1 { // deleted/inserted end boundary
		return s.O1
	}
	return s.O0 + (o - s.P0)
}

// ToOut maps an original offset to an output offset.
func (m *Map) ToOut(p int) int {
	i := m.find(func(s Segment) int { return s.O0 }, p)
	if i < 0 {
		return m.seg[0].P0
	}
	s := m.seg[i]
	if p >= s.O1 { // deleted run: clamp to the produced boundary
		return s.P1
	}
	return s.P0 + (p - s.O0)
}
