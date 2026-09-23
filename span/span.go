// Package span records original/output interval correspondences with binary
// bidirectional offset queries over half-open [0,len] positions.
package span

type kind uint8

const (
	kIdent kind = iota // [o0,o1) == [i0,i1), preserved bytes
	kDel               // [i0,i1) deleted, no output bytes (o0==o1)
	kIns               // synthetic output with no original bytes (i0==i1)
)

// Map is an ordered, non-overlapping tiling of the original and output axes.
type Map struct {
	r          []seg
	lastProbes int // intervals inspected by the most recent query
}

type seg struct {
	o0, o1 int
	i0, i1 int
	k      kind
}

// Builder constructs a Map left to right.
type Builder struct{ m Map }

func (b *Builder) Map() *Map   { return &b.m }
func (m *Map) NIntervals() int { return len(m.r) }
func (m *Map) LastProbes() int { return m.lastProbes }

func (b *Builder) push(s seg) {
	if n := len(b.m.r); n > 0 {
		p := &b.m.r[n-1]
		same := s.k == kIdent && p.k == kIdent || s.k == kDel && p.k == kDel
		if same && p.o1 == s.o0 && p.i1 == s.i0 {
			p.o1, p.i1 = s.o1, s.i1
			return
		}
	}
	b.m.r = append(b.m.r, s)
}

func (b *Builder) tip() seg {
	if n := len(b.m.r); n > 0 {
		return b.m.r[n-1]
	}
	return seg{}
}

// Copy records n preserved bytes advancing both axes.
func (b *Builder) Copy(n int) {
	p := b.tip()
	b.push(seg{p.o1, p.o1 + n, p.i1, p.i1 + n, kIdent})
}

// Delete records n original bytes removed at the current output position.
func (b *Builder) Delete(n int) {
	p := b.tip()
	b.push(seg{p.o1, p.o1, p.i1, p.i1 + n, kDel})
}

// Insert records n synthetic output bytes at original endpoint iEnd.
func (b *Builder) Insert(n, iEnd int) {
	p := b.tip()
	b.push(seg{p.o1, p.o1 + n, iEnd, iEnd, kIns})
}

// Retract removes the trailing n output bytes and records the equivalent
// original interval as deleted (used when trimming final newlines).
func (b *Builder) Retract(n int) {
	for n > 0 && len(b.m.r) > 0 {
		s := &b.m.r[len(b.m.r)-1]
		d := s.o1 - s.o0
		if d != 0 && d <= n {
			*s = seg{s.o0, s.o0, s.i0, s.i1, kDel}
			n -= d
			continue
		}
		break
	}
	for j := 1; j < len(b.m.r); j++ { // coalesce adjacent deletions
		if b.m.r[j].k == kDel && b.m.r[j-1].k == kDel && b.m.r[j-1].i1 == b.m.r[j].i0 {
			b.m.r[j-1].i1 = b.m.r[j].i1
			b.m.r = append(b.m.r[:j], b.m.r[j+1:]...)
			j--
		}
	}
}

// ToOrig maps an output offset o in [0,outLen] back to an original offset.
func (m *Map) ToOrig(o int) int {
	lo, _ := m.search(o, true)
	if lo == len(m.r) {
		return m.end(lo, true)
	}
	s := m.r[lo]
	if s.k == kIdent {
		return s.i0 + (o - s.o0)
	}
	if s.k == kIns && o == s.o0 && lo > 0 {
		return m.r[lo-1].i1
	}
	return s.i0 // interior of an inserted interval
}

// ToOut maps an original offset i in [0,origLen] to an output offset.
func (m *Map) ToOut(i int) int {
	lo, _ := m.search(i, false)
	if lo == len(m.r) {
		return m.end(lo, false)
	}
	s := m.r[lo]
	if s.k == kIdent {
		return s.o0 + (i - s.i0)
	}
	return s.o1 // kDel: deleted bytes land just after the deletion
}

func (m *Map) end(lo int, orig bool) int {
	if lo == 0 {
		return 0
	}
	s := m.r[lo-1]
	if orig {
		return s.i1
	}
	return s.o1
}

// search returns the first interval whose end (output when byOut) exceeds x,
// plus the number of intervals inspected.
func (m *Map) search(x int, byOut bool) (int, int) {
	lo, hi, pr := 0, len(m.r), 0
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		pr++
		end := m.r[mid].i1
		if byOut {
			end = m.r[mid].o1
		}
		if end <= x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastProbes = pr
	return lo, pr
}
