// Package span records correspondences between original byte intervals and
// normalized-output byte intervals and answers bidirectional offset queries
// by binary search.
package span

type kind uint8

const (
	kKept kind = iota // bytes preserved: orig len == out len
	kDel              // deleted orig bytes: out length 0, ToOut -> p0
	kSyn              // synthetic output bytes: orig length 0, ToOrig -> o0
)

// Seg is one correspondence interval, half-open on both axes.
type Seg struct {
	o0, o1 int // original [o0,o1)
	p0, p1 int // output   [p0,p1)
	k      kind
}

// Map is an immutable-ish correspondence table built by Builder.
type Map struct {
	segs []Seg
	// lastCheck is the number of mapping intervals examined by the most
	// recent ToOrig/ToOut query (binary-search probes).
	lastCheck int
}

// Segments reports how many intervals the table holds.
func (m *Map) Segments() int { return len(m.segs) }

// LastCheck reports intervals examined by the last query.
func (m *Map) LastCheck() int { return m.lastCheck }

// OrigLen is the exclusive end of the original domain.
func (m *Map) OrigLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	return m.segs[len(m.segs)-1].o1
}

// OutLen is the exclusive end of the output domain.
func (m *Map) OutLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	return m.segs[len(m.segs)-1].p1
}

// ToOrig maps an output offset o in [0,OutLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	lo, hi := 0, len(m.segs)
	m.lastCheck = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastCheck++
		if m.segs[mid].p1 <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.segs) {
		return m.OrigLen()
	}
	s := m.segs[lo]
	if s.k == kSyn {
		return s.o0
	}
	return s.o0 + (o - s.p0)
}

// ToOut maps an original offset i in [0,OrigLen] to an output offset.
func (m *Map) ToOut(i int) int {
	lo, hi := 0, len(m.segs)
	m.lastCheck = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastCheck++
		if m.segs[mid].o1 <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.segs) {
		return m.OutLen()
	}
	s := m.segs[lo]
	switch {
	case s.k == kSyn && s.o0 == i:
		return s.p0
	case s.k == kDel:
		return s.p0
	default:
		return s.p0 + (i - s.o0)
	}
}

// Builder incrementally constructs a Map while bytes stream through.
type Builder struct{ m Map }

// NewBuilder returns an empty builder.
func NewBuilder() *Builder { return &Builder{} }

func (b *Builder) outLen() int {
	if n := len(b.m.segs); n > 0 {
		return b.m.segs[n-1].p1
	}
	return 0
}

// Keep records one original byte at off that survives at the current output end.
func (b *Builder) Keep(off int) {
	p := b.outLen()
	if n := len(b.m.segs); n > 0 {
		s := &b.m.segs[n-1]
		if s.k == kKept && s.o1 == off && s.p1 == p {
			s.o1, s.p1 = off+1, p+1
			return
		}
	}
	b.m.segs = append(b.m.segs, Seg{off, off + 1, p, p + 1, kKept})
}

// Drop records one deleted original byte at off mapped to output position at.
func (b *Builder) Drop(off, at int) {
	if n := len(b.m.segs); n > 0 {
		s := &b.m.segs[n-1]
		if s.k == kDel && s.o1 == off && s.p0 == at {
			s.o1 = off + 1
			return
		}
	}
	b.m.segs = append(b.m.segs, Seg{off, off + 1, at, at, kDel})
}

// TrimTrailingNewlines removes all surviving trailing \n segments and returns
// the original offset of the first removed newline. -1 if there were none.
func (b *Builder) TrimTrailingNewlines() int {
	first := -1
	for len(b.m.segs) > 0 {
		s := &b.m.segs[len(b.m.segs)-1]
		if s.k != kKept || s.o1-s.o0 != 1 {
			break
		}
		first = s.o0
		b.m.segs = b.m.segs[:len(b.m.segs)-1]
	}
	return first
}

// SyntheticNL appends one output \n anchored at original point origPoint.
func (b *Builder) SyntheticNL(origPoint int) {
	p := b.outLen()
	b.m.segs = append(b.m.segs, Seg{origPoint, origPoint, p, p + 1, kSyn})
}

// Build returns the accumulated map.
func (b *Builder) Build() *Map { return &b.m }

// Concat joins per-part maps, translating each part's coordinates by its
// global original/output start offset. origOff[i], outOff[i] are the starts.
func Concat(parts []*Map, origOff, outOff []int) *Map {
	out := &Map{}
	for i, m := range parts {
		oo, pp := origOff[i], outOff[i]
		for _, s := range m.segs {
			ns := Seg{s.o0 + oo, s.o1 + oo, s.p0 + pp, s.p1 + pp, s.k}
			if n := len(out.segs); n > 0 {
				ps := &out.segs[n-1]
				if ps.k == kKept && ns.k == kKept && ps.o1 == ns.o0 && ps.p1 == ns.p0 {
					ps.o1, ps.p1 = ns.o1, ns.p1
					continue
				}
			}
			out.segs = append(out.segs, ns)
		}
	}
	return out
}
