// Package span records the correspondence between original byte ranges and
// output byte ranges and answers bidirectional offset queries in logarithmic
// time. Segments tile [0, OrigLen) without gaps; a dropped range has zero
// output length. It has no dependencies on other packages.
package span

// Builder incrementally constructs a mapping while a stream is normalized.
type Builder struct {
	seg []seg
	oc  int // current original offset
	pc  int // current output offset
}

type seg struct {
	i0, i1 int // original [i0,i1)
	o0, o1 int // output  [o0,o1); equal for dropped ranges
}

// Retain records n bytes that pass through unchanged.
func (b *Builder) Retain(n int) {
	if n <= 0 {
		return
	}
	if m := len(b.seg); m > 0 && b.seg[m-1].o1 == b.pc {
		s := &b.seg[m-1]
		if s.i1 == b.oc && s.i1-s.i0 == s.o1-s.o0 {
			s.i1 += n
			s.o1 += n
			b.oc += n
			b.pc += n
			return
		}
	}
	b.seg = append(b.seg, seg{b.oc, b.oc + n, b.pc, b.pc + n})
	b.oc += n
	b.pc += n
}

// Drop records n original bytes deleted at the current output offset.
func (b *Builder) Drop(n int) {
	if n <= 0 {
		return
	}
	if m := len(b.seg); m > 0 {
		if s := &b.seg[m-1]; s.i1 == b.oc && s.o0 == s.o1 {
			s.i1 += n
			b.oc += n
			return
		}
	}
	b.seg = append(b.seg, seg{b.oc, b.oc + n, b.pc, b.pc})
	b.oc += n
}

// Retract removes nTrail trailing output bytes (must be retained 1:1 bytes)
// and returns how many original bytes were removed.
func (b *Builder) Retract(nTrail int) int {
	rem := 0
	for nTrail > 0 && len(b.seg) > 0 {
		s := &b.seg[len(b.seg)-1]
		take := s.o1 - s.o0
		if take > nTrail {
			take = nTrail
		}
		s.i1 -= take
		s.o1 -= take
		b.oc -= take
		b.pc -= take
		rem += take
		nTrail -= take
		if s.i0 == s.i1 {
			b.seg = b.seg[:len(b.seg)-1]
		}
	}
	return rem
}

// OrigLen is the total original length recorded so far.
func (b *Builder) OrigLen() int { return b.oc }

// OutLen is the total output length recorded so far.
func (b *Builder) OutLen() int { return b.pc }

// Segments is the number of mapping segments.
func (b *Builder) Segments() int { return len(b.seg) }

// Translate shifts every coordinate by di (original) and do (output).
func (b *Builder) Translate(di, do int) {
	for k := range b.seg {
		s := &b.seg[k]
		s.i0 += di
		s.i1 += di
		s.o0 += do
		s.o1 += do
	}
	b.oc += di
	b.pc += do
}

// Append concatenates other, whose coordinates must continue this builder.
func (b *Builder) Append(other *Builder) {
	b.seg = append(b.seg, other.seg...)
	b.oc = other.oc
	b.pc = other.pc
}

// Mapper freezes a Builder into a queryable mapping. insOrig/insOut >= 0 mark
// one synthetic inserted output byte (used by the trailing-newline policy).
func (b *Builder) Mapper(insOrig, insOut int) *Mapper {
	s := make([]seg, len(b.seg))
	copy(s, b.seg)
	return &Mapper{seg: s, origEnd: b.oc, outEnd: b.pc, insOrig: insOrig, insOut: insOut}
}

// Mapper answers bidirectional offset queries.
type Mapper struct {
	seg                       []seg
	origEnd, outEnd           int
	insOrig, insOut           int // -1 when no inserted byte
	probes                    int
}

// LastProbes reports how many segments the most recent query inspected.
func (m *Mapper) LastProbes() int { return m.probes }

// Segments is the mapping size.
func (m *Mapper) Segments() int { return len(m.seg) }

// OrigLen is the original length (domain endpoint).
func (m *Mapper) OrigLen() int { return m.origEnd }

// OutLen is the output length (range endpoint).
func (m *Mapper) OutLen() int {
	n := m.outEnd
	if m.insOrig >= 0 {
		n++
	}
	return n

}

func (m *Mapper) find(hi int, end func(seg) int, v int) int {
	lo := 0
	m.probes = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.probes++
		if end(m.seg[mid]) > v {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// ToOrig maps an output offset in [0, OutLen] to an original offset.
func (m *Mapper) ToOrig(o int) int {
	if m.insOrig >= 0 && o >= m.insOut {
		if o == m.insOut {
			return m.insOrig
		}
		o--
	}
	k := m.find(len(m.seg), func(s seg) int { return s.o1 }, o)
	s := m.seg[k]
	if s.o1-s.o0 > 0 && o < s.o1 {
		return s.i0 + (o - s.o0)
	}
	return s.i1
}

// ToOut maps an original offset in [0, OrigLen] to an output offset.
func (m *Mapper) ToOut(i int) int {
	if i == m.origEnd {
		return m.OutLen()
	}
	k := m.find(len(m.seg), func(s seg) int { return s.i1 }, i)
	s := m.seg[k]
	if s.i1-s.i0 > 0 && i < s.i1 {
		return s.o0 + (i - s.i0)
	}
	return s.o1
}

// Slice returns a builder holding the mapping for original range [a,b),
// renumbered so both coordinates start at zero. Segments straddling a or b
// are cut at byte boundaries.
func (m *Mapper) Slice(a, b int) *Builder {
	out := &Builder{}
	add := func(s seg) {
		if s.i1 <= a || s.i0 >= b {
			return
		}
		i0, i1 := s.i0, s.i1
		o0, o1 := s.o0, s.o1
		if i0 < a {
			x := a - i0
			if i1-i0 == o1-o0 {
				o0 += x
			}
			i0 = a
		}
		if i1 > b {
			x := i1 - b
			if i1-i0 == o1-o0 {
				o1 -= x
			}
			i1 = b
		}
		if i1 <= i0 {
			return
		}
		out.seg = append(out.seg, seg{i0 - a, i1 - a, o0, o1})
	}
	for _, s := range m.seg {
		add(s)
	}
	if n := len(out.seg); n > 0 {
		base := out.seg[0].o0
		for k := range out.seg {
			out.seg[k].o0 -= base
			out.seg[k].o1 -= base
		}
		out.oc = out.seg[n-1].i1
		out.pc = out.seg[n-1].o1
	}
	return out
}
