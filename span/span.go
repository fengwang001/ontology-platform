// Package span records a piecewise mapping between original and normalized
// byte offsets. Segments are kept in order and coalesced.
package span

// Kind of a mapping segment.
const (
	KindKeep = iota // [a,b) <-> [c,c+(b-a)) one-to-one
	KindDropOrig    // [a,b) deleted: all map to output point c
	KindDropOut     // output [c,d) inserted with no original bytes
)

// Seg is one mapping segment. Exactly one interval may be empty.
type Seg struct {
	Kind      byte
	A0, A1 int // original interval [A0,A1)
	C0, C1 int // output interval [C0,C1)
}

// Map is an immutable offset map.
type Map struct {
	segs    []Seg
	origLen int
	outLen  int
	checked int // segments inspected by the latest query
}

// LastChecked reports how many segments the most recent ToOrig/ToOut call
// inspected (binary-search probe count).
func (m *Map) LastChecked() int { return m.checked }

// LenOrig and LenOut report mapped domain sizes.
func (m *Map) LenOrig() int { return m.origLen }
func (m *Map) LenOut() int  { return m.outLen }

// NumSegs reports the number of stored segments.
func (m *Map) NumSegs() int { return len(m.segs) }

// Segs returns a copy of the mapping segments.
func (m *Map) Segs() []Seg {
	out := make([]Seg, len(m.segs))
	copy(out, m.segs)
	return out
}

// ToOrig maps an output offset o in [0,LenOut] to an original offset.
func (m *Map) ToOrig(o int) int {
	m.checked = 0
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.checked++
		mid := int(uint(lo+hi) >> 1)
		s := m.segs[mid]
		if o < s.C0 || (o == s.C0 && s.Kind == KindDropOut) {
			hi = mid
			continue
		}
		if o > s.C1 || (o == s.C1 && s.Kind != KindDropOut) {
			lo = mid + 1
			continue
		}
		switch s.Kind {
		case KindKeep:
			return s.A0 + (o - s.C0)
		case KindDropOrig:
			return s.A1
		default:
			return s.A0
		}
	}
	return m.origLen
}

// ToOut maps an original offset i in [0,LenOrig] to an output offset.
func (m *Map) ToOut(i int) int {
	m.checked = 0
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.checked++
		mid := int(uint(lo+hi) >> 1)
		s := m.segs[mid]
		if i < s.A0 || (i == s.A0 && s.Kind == KindDropOut) {
			hi = mid
			continue
		}
		if i > s.A1 || (i == s.A1 && s.Kind != KindDropOut) {
			lo = mid + 1
			continue
		}
		switch s.Kind {
		case KindKeep:
			return s.C0 + (i - s.A0)
		case KindDropOrig:
			return s.C0
		default:
			return s.C1
		}
	}
	return m.outLen
}

// Builder incrementally constructs a Map left to right.
type Builder struct {
	segs    []Seg
	origLen int
	outLen  int
}

// Keep appends n one-to-one bytes.
func (b *Builder) Keep(n int) {
	if n <= 0 {
		return
	}
	b.segs = append(b.segs, Seg{KindKeep, b.origLen, b.origLen + n, b.outLen, b.outLen + n})
	b.origLen += n
	b.outLen += n
}

// DropOrig appends n deleted original bytes collapsing onto the next output point.
func (b *Builder) DropOrig(n int) {
	if n <= 0 {
		return
	}
	b.segs = append(b.segs, Seg{KindDropOrig, b.origLen, b.origLen + n, b.outLen, b.outLen})
	b.origLen += n
}

// AppendSegs appends already-built segments shifted by da (original) and dc (output).
func (b *Builder) AppendSegs(segs []Seg, da, dc int) {
	for _, s := range segs {
		s.A0 += da
		s.A1 += da
		s.C0 += dc
		s.C1 += dc
		b.segs = append(b.segs, s)
	}
	if len(segs) > 0 {
		last := segs[len(segs)-1]
		b.origLen = last.A1 + da
		b.outLen = last.C1 + dc
	}
}

// ShrinkOut truncates the output to n bytes, mapping the removed suffix to a
// single collapse point (used by the Trim final-newline policy).
func (b *Builder) ShrinkOut(n int) {
	if n >= b.outLen {
		return
	}
	var kept []Seg
	for _, s := range b.segs {
		if s.C1 <= n {
			kept = append(kept, s)
			continue
		}
		switch s.Kind {
		case KindKeep:
			t := n - s.C0
			if t > 0 {
				kept = append(kept, Seg{KindKeep, s.A0, s.A0 + t, s.C0, n})
			}
			kept = append(kept, Seg{KindDropOrig, s.A0 + t, s.A1, n, n})
		case KindDropOrig:
			s.C0, s.C1 = n, n
			kept = append(kept, s)
		default:
			s.C0, s.C1 = n, n
			kept = append(kept, s)
		}
	}
	b.segs = kept
	b.outLen = n
}

// GrowOut inserts n output-only bytes at the current end (used by EnsureOne).
func (b *Builder) GrowOut(n int) {
	if n <= 0 {
		return
	}
	b.segs = append(b.segs, Seg{KindDropOut, b.origLen, b.origLen, b.outLen, b.outLen + n})
	b.outLen += n
}

// Build freezes the builder into a Map, coalescing mergeable neighbors.
func (b *Builder) Build() *Map {
	var out []Seg
	for _, s := range b.segs {
		if n := len(out); n > 0 {
			p := out[n-1]
			same := p.Kind == s.Kind && p.Kind != KindDropOut &&
				p.A1 == s.A0 && p.C1 == s.C0
			if same {
				out[n-1].A1 = s.A1
				out[n-1].C1 = s.C1
				continue
			}
		}
		out = append(out, s)
	}
	return &Map{segs: out, origLen: b.origLen, outLen: b.outLen}
}
