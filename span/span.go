// Package span records the correspondence between original byte ranges and
// normalized output byte ranges and answers both mapping directions.
package span

// kind: 0 retain (same offsets), 1 delete (orig only, collapses forward),
// 2 insert (out only, backward anchor at d0).
type seg struct {
	kind           uint8
	o0, o1, d0, d1 int
}

// Map is an append-only, coalesced set of mapping segments covering both
// [0, OrigLen) and [0, OutLen), endpoints included. Not concurrency-safe.
type Map struct {
	segs []seg
	last int64
}

func (m *Map) tail() *seg {
	if len(m.segs) == 0 {
		return nil
	}
	return &m.segs[len(m.segs)-1]
}

// Copy records n emitted bytes ending at outOff, sourced from orig d0.
func (m *Map) Copy(d0, n, outOff int) {
	if n <= 0 {
		return
	}
	if t := m.tail(); t != nil && t.kind == 0 && t.o1 == outOff-n && t.d1 == d0 {
		t.o1 = outOff
		t.d1 = d0 + n
		return
	}
	m.segs = append(m.segs, seg{0, outOff - n, outOff, d0, d0 + n})
}

// Delete records original [d0,d1) producing no output; it collapses onto o.
func (m *Map) Delete(d0, d1, outOff int) {
	if d1 <= d0 {
		return
	}
	if t := m.tail(); t != nil && t.kind == 1 && t.o0 == outOff && t.d1 == d0 {
		t.d1 = d1
		return
	}
	m.segs = append(m.segs, seg{1, outOff, outOff, d0, d1})
}

// Insert records output [o0,o1) with no source bytes, anchored at orig dOff.
func (m *Map) Insert(dOff, o0, o1 int) {
	if o1 <= o0 {
		return
	}
	if t := m.tail(); t != nil && t.kind == 2 && t.d0 == dOff && t.o1 == o0 {
		t.o1 = o1
		return
	}
	m.segs = append(m.segs, seg{2, o0, o1, dOff, dOff})
}

// Truncate drops coverage strictly after (origEnd,outEnd).
func (m *Map) Truncate(origEnd, outEnd int) {
	for i := len(m.segs) - 1; i >= 0; i-- {
		s := m.segs[i]
		switch {
		case s.kind == 0 && s.o0 >= outEnd:
			m.segs = m.segs[:i]
		case s.kind == 0 && s.o1 > outEnd:
			s.o1, s.d1 = outEnd, s.d0+(outEnd-s.o0)
			m.segs[i] = s
			return
		case s.kind == 1 && s.d0 >= origEnd:
			m.segs = m.segs[:i]
		case s.kind == 2 && s.o0 >= outEnd:
			m.segs = m.segs[:i]
		default:
			return
		}
	}
}

func (m *Map) find(n int, after func(seg) bool) int {
	lo, hi := 0, n
	m.last = 0
	for lo < hi {
		m.last++
		mid := int(uint(lo+hi) >> 1)
		if after(m.segs[mid]) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// ToOrig maps an output offset in [0,OutLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	n := len(m.segs)
	i := m.find(n, func(s seg) bool { return s.o0 > o })
	m.last++
	if i < n && m.segs[i].o0 == o {
		if s := m.segs[i]; s.kind != 1 {
			return s.d0
		}
	}
	if i == 0 {
		return 0
	}
	s := m.segs[i-1]
	if s.kind == 0 {
		return s.d0 + (o - s.o0)
	}
	if s.kind == 1 {
		return s.d1
	}
	return s.d0
}

// ToOut maps an original offset in [0,OrigLen] to an output offset. Deleted
// bytes collapse forward onto the first output position after the deletion.
func (m *Map) ToOut(d int) int {
	n := len(m.segs)
	i := m.find(n, func(s seg) bool { return s.d0 > d })
	m.last++
	if i < n && m.segs[i].d0 == d {
		if s := m.segs[i]; s.kind != 1 {
			if s.kind == 2 {
				return s.o1
			}
			return s.o0
		}
	}
	if i == 0 {
		return 0
	}
	s := m.segs[i-1]
	if s.kind == 0 {
		return s.o0 + (d - s.d0)
	}
	return s.o1
}

// LastChecks reports segments inspected by the latest query (binary search).
func (m *Map) LastChecks() int { return int(m.last) }

// Segments reports the stored segment count.
func (m *Map) Segments() int { return len(m.segs) }

// OrigLen is the original coverage end.
func (m *Map) OrigLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	if t := m.segs[len(m.segs)-1]; t.kind == 2 {
		return t.d0
	} else {
		return t.d1
	}
}

// OutLen is the output coverage end.
func (m *Map) OutLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	return m.segs[len(m.segs)-1].o1
}

// Concat joins per-piece maps, translating later pieces by prefix lengths.
func Concat(parts ...*Map) *Map {
	var out Map
	var do, dd int
	for _, p := range parts {
		for _, s := range p.segs {
			s.o0 += do
			s.o1 += do
			s.d0 += dd
			s.d1 += dd
			out.segs = append(out.segs, s)
		}
		do += p.OutLen()
		dd += p.OrigLen()
	}
	return &out
}
