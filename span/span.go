// Package span records byte-offset correspondence between an original byte
// stream and a normalized output stream and answers bidirectional queries.
// Segments [I0,I1)<-[O0,O1): identity (equal length), delete (O1==O0),
// insert (I1==I0).
package span

import "sort"

// Seg is one correspondence segment; ranges are left-closed, right-open.
type Seg struct{ I0, I1, O0, O1 int64 }

func (s Seg) kind() int {
	switch {
	case s.I1 > s.I0 && s.O1 > s.O0:
		return 0 // identity
	case s.I1 > s.I0:
		return 1 // delete
	default:
		return 2 // insert
	}
}

// Builder appends segments and coalesces adjacent same-kind segments, so the
// count grows with delete/insert points rather than byte count.
type Builder struct {
	segs   []Seg
	i0, o0 int64
	IL, OL int64
}

// NewBuilder returns an empty builder.
func NewBuilder() *Builder { return &Builder{} }

// Add appends the segment ending at original offset i1 and output offset o1.
func (b *Builder) Add(i1, o1 int64) {
	if i1 < b.i0 || o1 < b.o0 {
		panic("span: non-monotonic Add")
	}
	if i1 == b.i0 && o1 == b.o0 {
		return
	}
	s := Seg{I0: b.i0, I1: i1, O0: b.o0, O1: o1}
	if n := len(b.segs); n > 0 {
		last := &b.segs[n-1]
		if last.kind() == s.kind() && last.I1 == s.I0 && last.O1 == s.O0 {
			last.I1, last.O1 = s.I1, s.O1
			b.i0, b.o0, b.IL, b.OL = i1, o1, i1, o1
			return
		}
	}
	b.segs = append(b.segs, s)
	b.i0, b.o0, b.IL, b.OL = i1, o1, i1, o1
}

// TruncateOut drops segments past output offset o and shortens the segment
// containing o. Delete/insert segments at o collapse away.
func (b *Builder) TruncateOut(o int64) {
	if o >= b.OL {
		return
	}
	idx := sort.Search(len(b.segs), func(k int) bool { return b.segs[k].O1 > o })
	if idx == len(b.segs) {
		idx = len(b.segs) - 1
	}
	s := b.segs[idx]
	b.segs = b.segs[:idx]
	if s.kind() == 0 {
		s.I1 = s.I0 + (o - s.O0)
		s.O1 = o
		b.segs = append(b.segs, s)
	}
	b.i0, b.o0 = s.I0, o
	b.IL, b.OL = b.i0, o
}

func (b *Builder) Segs() []Seg { return b.segs }

func (b *Builder) ILen() int64 { return b.IL }

func (b *Builder) OLen() int64 { return b.OL }

// Build returns an immutable snapshot.
func (b *Builder) Build() *Map {
	out := make([]Seg, len(b.segs))
	copy(out, b.segs)
	return &Map{segs: out, il: b.IL, ol: b.OL}
}

// Map answers bidirectional queries via binary search.
type Map struct {
	segs      []Seg
	il, ol    int64
	lastCheck int
}

// NewMap creates a Map from a prebuilt partition.
func NewMap(segs []Seg, il, ol int64) *Map {
	return &Map{segs: segs, il: il, ol: ol}
}

func (m *Map) Segs() []Seg { return m.segs }

func (m *Map) ILen() int64 { return m.il }

func (m *Map) OLen() int64 { return m.ol }

func (m *Map) search(right func(Seg) int64, v int64) int {
	lo, hi, checked := 0, len(m.segs), 0
	for lo < hi {
		mid := (lo + hi) / 2
		checked++
		if right(m.segs[mid]) > v {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == len(m.segs) {
		lo--
	}
	m.lastCheck = checked
	return lo
}

// LastCheck is the number of segments inspected by the latest query.
func (m *Map) LastCheck() int { return m.lastCheck }

// ToOrig maps output offset o in [0,OLen] to an original offset.
func (m *Map) ToOrig(o int64) int64 {
	s := m.segs[m.search(func(s Seg) int64 { return s.O1 }, o)]
	if s.O1 == s.O0 { // delete segment: image is the next surviving position
		return s.I1
	}
	return s.I0 + (o - s.O0)
}

// ToOut maps original offset i in [0,ILen] to an output offset.
func (m *Map) ToOut(i int64) int64 {
	s := m.segs[m.search(func(s Seg) int64 { return s.I1 }, i)]
	switch {
	case s.I1 == s.I0: // insert segment
		return s.O1
	case s.O1 == s.O0: // delete segment: every deleted byte maps to the gap edge
		return s.O0
	}
	return s.O0 + (i - s.I0)
}
