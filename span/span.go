// Package span records the correspondence between original byte intervals and
// output byte intervals and answers both mapping directions by binary search.
// It depends on no other package.
package span

// Segment is an affine piece: original half-open [OrigLo, OrigHi) maps to
// output [OutLo, OutHi). A deletion is a piece with OutLo==OutHi.
type Segment struct {
	OrigLo, OrigHi int
	OutLo, OutHi   int
}

// Builder assembles segments and output bytes left to right.
type Builder struct {
	segs   []Segment
	out    []byte
	origAt int
	outAt  int
}

// Emit appends a 1:1 piece (e.g. lone CR -> '\n'); adjacent pieces merge.
func (b *Builder) Emit(p []byte) {
	n := len(p)
	if len(b.segs) > 0 {
		s := &b.segs[len(b.segs)-1]
		if s.OrigHi == b.origAt && s.OutHi == b.outAt &&
			s.OrigHi-s.OrigLo == s.OutHi-s.OutLo {
			s.OrigHi += n
			s.OutHi += n
			b.out = append(b.out, p...)
			b.origAt += n
			b.outAt += n
			return
		}
	}
	b.segs = append(b.segs, Segment{b.origAt, b.origAt + n, b.outAt, b.outAt + n})
	b.out = append(b.out, p...)
	b.origAt += n
	b.outAt += n
}

// Delete records n original bytes that produce no output.
func (b *Builder) Delete(n int) {
	if n == 0 {
		return
	}
	b.segs = append(b.segs, Segment{b.origAt, b.origAt + n, b.outAt, b.outAt})
	b.origAt += n
}

// AppendMap appends another piece list shifted into this coordinate space.
func (b *Builder) AppendMap(oOrig, oOut int, other []Segment, p []byte) {
	for _, s := range other {
		b.segs = append(b.segs, Segment{
			s.OrigLo + oOrig, s.OrigHi + oOrig, s.OutLo + oOut, s.OutHi + oOut,
		})
	}
	b.out = append(b.out, p...)
	if len(other) > 0 {
		b.origAt = other[len(other)-1].OrigHi + oOrig
	}
	b.outAt = oOut + len(p)
}

// Finish returns the immutable map and produced output.
func (b *Builder) Finish() (*Map, []byte) {
	return &Map{segs: b.segs}, b.out
}

// Map is an immutable two-directional offset map.
type Map struct {
	segs     []Segment
	lastHits int // probes spent by the most recent query
}

// Segments returns the underlying pieces for composition (e.g. by par).
func (m *Map) Segments() []Segment { return m.segs }

// LastProbes reports how many pieces the most recent query inspected.
func (m *Map) LastProbes() int { return m.lastHits }

// ToOrig maps an output offset o in [0,outLen] to an original offset.
// Deletion pieces (OutLo==OutHi) never satisfy OutHi>o, so at a junction the
// piece to the right is selected, giving ToOut(ToOrig(o))==o.
func (m *Map) ToOrig(o int) int {
	lo, hi, hits := 0, len(m.segs), 0
	for lo < hi {
		hits++
		mid := (lo + hi) / 2
		if m.segs[mid].OutHi > o {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	m.lastHits = hits
	switch {
	case lo == len(m.segs):
		if lo == 0 {
			return 0
		}
		return m.segs[lo-1].OrigHi
	default:
		s := m.segs[lo]
		return s.OrigLo + (o - s.OutLo)
	}
}

// ToOut maps an original offset i in [0,origLen] to an output offset.
// Inside a deletion piece every boundary collapses onto that piece's OutLo.
func (m *Map) ToOut(i int) int {
	lo, hi, hits := 0, len(m.segs), 0
	for lo < hi {
		hits++
		mid := (lo + hi) / 2
		if m.segs[mid].OrigHi >= i {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	m.lastHits = hits
	switch {
	case lo == len(m.segs):
		if lo == 0 {
			return 0
		}
		return m.segs[lo-1].OutHi
	default:
		s := m.segs[lo]
		if s.OrigHi-s.OrigLo == s.OutHi-s.OutLo {
			return s.OutLo + (i - s.OrigLo)
		}
		return s.OutLo
	}
}
}
