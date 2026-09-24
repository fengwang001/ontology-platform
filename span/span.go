// Package span records byte-offset correspondence between an original
// stream and a derived output, with binary-search bidirectional queries.
//
// Fragments are non-empty pieces of four shapes: keep (n->n), del
// (n->0), ins (0->n), rep (1->1). Count grows with edit points, not
// stream length. All intervals are half-open.
package span

import "sort"

// Fragment is one contiguous piece: original [O0,O1) <-> output [U0,U1).
type Fragment struct{ O0, O1, U0, U1 int }

// Builder appends ascending fragments.
type Builder struct {
	frags []Fragment
	o, u  int
}

// Fragments returns the recorded fragments.
func (b *Builder) Fragments() []Fragment { return b.frags }

func (b *Builder) keepMerge(f Fragment) bool {
	if n := len(b.frags); n > 0 {
		if p := &b.frags[n-1]; p.O1 == b.o && p.U1 == b.u &&
			(p.O1-p.O0) == (p.U1-p.U0) {
			p.O1, p.U1 = f.O1, f.U1
			b.o, b.u = f.O1, f.U1
			return true
		}
	}
	return false
}

// Keep appends n verbatim bytes (n -> n).
func (b *Builder) Keep(n int) {
	if n <= 0 {
		return
	}
	f := Fragment{b.o, b.o + n, b.u, b.u + n}
	if !b.keepMerge(f) {
		b.append(f)
	}
}

// Replace records one original byte replaced by one output byte.
func (b *Builder) Replace() {
	f := Fragment{b.o, b.o + 1, b.u, b.u + 1}
	if !b.keepMerge(f) {
		b.append(f)
	}
}

// Del records n deleted original bytes (n -> 0), merging with a prior del.
func (b *Builder) Del(n int) {
	if n <= 0 {
		return
	}
	if k := len(b.frags); k > 0 {
		if p := &b.frags[k-1]; p.U0 == p.U1 && p.U1 == b.u && p.O1 == b.o {
			p.O1 += n
			b.o += n
			return
		}
	}
	b.append(Fragment{b.o, b.o + n, b.u, b.u})
}

// Ins records n inserted output bytes (0 -> n).
func (b *Builder) Ins(n int) {
	if n > 0 {
		b.append(Fragment{b.o, b.o, b.u, b.u + n})
	}
}

// AppendFrag appends an externally prepared contiguous fragment.
func (b *Builder) AppendFrag(f Fragment) { b.append(f) }

func (b *Builder) append(f Fragment) {
	b.frags = append(b.frags, f)
	b.o, b.u = f.O1, f.U1
}

// TruncateOut truncates the output at absolute offset u; the removed
// original tail becomes a deletion. u must be a fragment boundary.
func (b *Builder) TruncateOut(u int) {
	if u < 0 || u > b.u {
		return
	}
	j := sort.Search(len(b.frags), func(i int) bool { return b.frags[i].U1 >= u })
	if j == len(b.frags) {
		return
	}
	f := b.frags[j]
	keepN := 0
	if f.U1 > f.U0 {
		keepN = u - f.U0
	}
	b.frags = b.frags[:j]
	if keepN > 0 {
		b.frags = append(b.frags, Fragment{f.O0, f.O0 + keepN, f.U0, u})
	}
	if d := f.O1 - f.O0 - keepN; d > 0 {
		b.frags = append(b.frags, Fragment{f.O0 + keepN, f.O1, u, u})
	}
	b.u, b.o = u, f.O1
}

// Build freezes the fragments into an immutable Map.
func (b *Builder) Build() *Map {
	out := make([]Fragment, len(b.frags))
	copy(out, b.frags)
	return &Map{frags: out, origLen: b.o, outLen: b.u}
}

// Map is an immutable offset correspondence.
type Map struct {
	frags           []Fragment
	origLen, outLen int
	lastChecked     int
}

func (m *Map) OrigLen() int { return m.origLen }
func (m *Map) OutLen() int  { return m.outLen }
func (m *Map) Fragments() []Fragment { return m.frags }

// LastChecked reports fragments inspected by the most recent query.
func (m *Map) LastChecked() int { return m.lastChecked }

// ToOrig maps output offset o (0..OutLen) to an original offset.
func (m *Map) ToOrig(o int) int {
	if o < 0 || o > m.outLen {
		return -1
	}
	lo, hi, probes := 0, len(m.frags), 0
	for lo < hi {
		probes++
		mid := int(uint(lo+hi) >> 1)
		if m.frags[mid].U1 < o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecked = probes
	if lo == len(m.frags) {
		return m.origLen
	}
	f := m.frags[lo]
	if o > f.U0 && o < f.U1 && f.O1 > f.O0 {
		return f.O0 + (o - f.U0)
	}
	if o == f.U0 {
		return f.O0
	}
	return f.O1
}

// ToOut maps original offset i (0..OrigLen) to an output offset.
func (m *Map) ToOut(i int) int {
	if i < 0 || i > m.origLen {
		return -1
	}
	lo, hi, probes := 0, len(m.frags), 0
	for lo < hi {
		probes++
		mid := int(uint(lo+hi) >> 1)
		if m.frags[mid].O1 < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecked = probes
	if lo == len(m.frags) {
		return m.outLen
	}
	f := m.frags[lo]
	if i < f.O1 && f.U1 > f.U0 {
		return f.U0 + (i - f.O0)
	}
	return f.U1
}
