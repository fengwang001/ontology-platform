// Package span records the correspondence between original byte ranges and
// normalized output byte ranges and answers bidirectional offset queries by
// binary search. Segments come in three flavors:
//
//	ident  original [A,B) <-> output [C,D), equal length
//	delete original [A,B) collapses to the single output point C
//	insert output [C,D) (D-C is 1 here) hangs at original point A
//
// Adjacent identity ranges are merged, so segment count grows only with the
// number of deletion/insertion points, not with output size.
package span

// Kind classifies a mapping segment.
type Kind uint8

const (
	SegIdent Kind = iota
	SegDelete
	SegInsert
)

// Seg is one mapping segment.
type Seg struct {
	Kind     Kind
	A, B     int // original [A,B); insert uses the single point A == B
	C, D     int // output [C,D); delete uses the single point C == D
}

// Builder appends segments in stream order.
type Builder struct {
	segs []Seg
	oa   int // next original offset
	oc   int // next output offset
}

// Ident records n bytes passed through unchanged.
func (b *Builder) Ident(n int) {
	if n == 0 {
		return
	}
	if k := len(b.segs) - 1; k >= 0 && b.segs[k].Kind == SegIdent && b.segs[k].B == b.oa && b.segs[k].D == b.oc {
		b.segs[k].B += n
		b.segs[k].D += n
	} else {
		b.segs = append(b.segs, Seg{Kind: SegIdent, A: b.oa, B: b.oa + n, C: b.oc, D: b.oc + n})
	}
	b.oa += n
	b.oc += n
}

// Delete records n original bytes that map to the current output point.
func (b *Builder) Delete(n int) {
	if n == 0 {
		return
	}
	b.segs = append(b.segs, Seg{Kind: SegDelete, A: b.oa, B: b.oa + n, C: b.oc, D: b.oc})
	b.oa += n
}

// Insert records one emitted output byte at the current original point.
func (b *Builder) Insert() {
	b.segs = append(b.segs, Seg{Kind: SegInsert, A: b.oa, B: b.oa, C: b.oc, D: b.oc + 1})
	b.oc++
}

// Build freezes the map.
func (b *Builder) Build() *Map {
	return &Map{segs: b.segs, origLen: b.oa, outLen: b.oc}
}

// Truncate rewinds the builder to original/orig offsets ao,oo, dropping any
// segments that extend past them. It is used to discard an undecided tail
// before re-normalizing across a parallel chunk boundary.
func (b *Builder) Truncate(ao, oo int) {
	k := len(b.segs)
	for k > 0 && b.segs[k-1].A >= ao {
		k--
	}
	b.segs = b.segs[:k]
	b.oa, b.oc = ao, oo
}

// NewMap builds a map from an externally assembled segment slice.
func NewMap(segs []Seg, origLen, outLen int) *Map {
	return &Map{segs: segs, origLen: origLen, outLen: outLen}
}

// Map is an immutable bidirectional offset map.
type Map struct {
	segs       []Seg
	origLen    int
	outLen     int
	lastChecks int
}

// Segs returns the underlying segments (read-only).
func (m *Map) Segs() []Seg { return m.segs }

// OrigLen is the original byte length.
func (m *Map) OrigLen() int { return m.origLen }

// OutLen is the output byte length.
func (m *Map) OutLen() int { return m.outLen }

// LastChecks reports how many segment bounds the last query inspected.
func (m *Map) LastChecks() int { return m.lastChecks }

// hi is the largest output offset owned by segment s.
func hi(s Seg) int {
	if s.Kind == SegInsert {
		return s.D // insert owns only its end point (start is the predecessor's boundary)
	}
	if s.Kind == SegDelete {
		return s.C
	}
	return s.D - 1
}

// ToOrig maps an output offset o in [0,OutLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	lo, top := 0, len(m.segs)
	checks := 0
	for lo < top {
		checks++
		mid := (lo + top) / 2
		if h := hi(m.segs[mid]); h < o {
			lo = mid + 1
		} else {
			top = mid
		}
	}
	m.lastChecks = checks + 1
	s := m.segs[lo]
	switch s.Kind {
	case SegDelete:
		return s.A
	case SegInsert:
		return s.A
	default:
		return s.A + (o - s.C)
	}
}

// origHi is the largest original offset owned by segment s.
func origHi(s Seg) int {
	if s.Kind == SegInsert {
		return s.A // insert owns the point just after its predecessor
	}
	return s.B - 1
}

// ToOut maps an original offset i in [0,OrigLen] to an output offset.
func (m *Map) ToOut(i int) int {
	lo, top := 0, len(m.segs)
	checks := 0
	for lo < top {
		checks++
		mid := (lo + top) / 2
		if origHi(m.segs[mid]) < i {
			lo = mid + 1
		} else {
			top = mid
		}
	}
	m.lastChecks = checks + 1
	s := m.segs[lo]
	switch s.Kind {
	case SegDelete:
		return s.C
	case SegInsert:
		return s.D
	default:
		return s.C + (i - s.A)
	}
}
