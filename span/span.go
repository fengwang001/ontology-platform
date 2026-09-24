// Package span records correspondences between original byte ranges and
// normalized-output byte ranges and answers offset queries both ways.
// Segments are only: 1:1 kept/remapped ranges, original-only deletions, and a
// terminal synthetic fallback. Adjacent same-shape segments coalesce, so the
// segment count grows with deletion points, not with output size.
package span

type seg struct {
	a, b int // original [a,b)
	s, e int // output   [s,e)
}

// Mapper is an append-only correspondence table with coordinate rollback.
type Mapper struct {
	segs   []seg
	ol, pl int  // total original / output length
	checks int  // segments inspected by the latest query
	bar    bool // forbid coalescing into the last segment
}

// Keep records n bytes copied unchanged.
func (m *Mapper) Keep(n int) { m.add(n, n) }

// Remap records n bytes rewritten 1:1 (e.g. '\r' -> '\n').
func (m *Mapper) Remap(n int) { m.add(n, n) }

// Delete records n original bytes with no output.
func (m *Mapper) Delete(n int) { m.add(n, 0) }

func (m *Mapper) add(no, np int) {
	if no == 0 && np == 0 {
		return
	}
	if k := len(m.segs) - 1; k >= 0 {
		l := &m.segs[k]
		if !m.bar && (np == 0) == (l.e-l.s == 0) && l.b == m.ol && l.e == m.pl &&
			(no > 0 && np > 0) == (l.b-l.a > 0 && l.e-l.s > 0) {
			l.b += no
			l.e += np
			m.ol += no
			m.pl += np
			return
		}
	}
	m.segs = append(m.segs, seg{m.ol, m.ol + no, m.pl, m.pl + np})
	m.ol += no
	m.pl += np
	// bar remains sticky until Barrier is re-armed/cleared by the producer.
	// Producers call Barrier only after a deletion decision is closed.
}

// Barrier separates two decisions: later deletions never coalesce across it.
func (m *Mapper) Barrier() { m.bar = true }

// Borrow reclaims the last deleted original byte as a 1:1 remapped byte.
// It reports whether such a byte existed.
func (m *Mapper) Borrow() bool {
	for k := len(m.segs) - 1; k >= 0; k-- {
		l := &m.segs[k]
		if l.b-l.a > l.e-l.s && l.b == m.ol {
			l.b--
			m.ol--
			m.Remap(1)
			return true
		}
	}
	return false
}

// Synth records one output byte with no original byte, pinned at original
// offset off (used only when a trailing newline must be invented with no
// deletable source byte).
func (m *Mapper) Synth(off int) { m.segs = append(m.segs, seg{off, off, m.pl, m.pl + 1}); m.pl++ }

// TrimOutput removes k trailing output bytes. The underlying original bytes
// remain in the coordinate domain and become deletions; it returns the
// original offset of the cut point.
func (m *Mapper) TrimOutput(k int) int {
	if k <= 0 {
		return m.ol
	}
	total := m.ol
	newPl := m.pl - k
	lo, hi := 0, len(m.segs)
	for lo < hi {
		mid := (lo + hi) / 2
		if m.segs[mid].e <= newPl {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	var cut int
	if lo < len(m.segs) && m.segs[lo].s < newPl {
		s := &m.segs[lo]
		cut = s.a + (newPl - s.s)
		s.b = cut
		s.e = newPl
		m.segs = m.segs[:lo+1]
	} else {
		m.segs = m.segs[:lo]
		if g := len(m.segs) - 1; g >= 0 {
			cut = m.segs[g].b
		}
	}
	m.ol = cut
	m.pl = newPl
	m.add(total-cut, 0)
	return cut
}

// Translate shifts every coordinate by (do, dp) and returns m.
func (m *Mapper) Translate(do, dp int) *Mapper {
	for i := range m.segs {
		m.segs[i].a += do
		m.segs[i].b += do
		m.segs[i].s += dp
		m.segs[i].e += dp
	}
	m.ol += do
	m.pl += dp
	return m
}

// Merge appends other's segments; coordinates must already line up.
func (m *Mapper) Merge(o *Mapper) {
	for _, s := range o.segs {
		m.add(s.b-s.a, s.e-s.s)
	}
}

// OrigLen and OutLen are current coordinate extents.
func (m *Mapper) OrigLen() int { return m.ol }
func (m *Mapper) OutLen() int  { return m.pl }

// Segments is the table size.
func (m *Mapper) Segments() int { return len(m.segs) }

// Checks is the number of segments inspected by the latest query.
func (m *Mapper) Checks() int { return m.checks }

// Debug exposes the segment table for tests.
func (m *Mapper) Debug() []seg { return append([]seg(nil), m.segs...) }

// ToOrig maps an output offset to an original offset.
func (m *Mapper) ToOrig(o int) int {
	m.checks = 0
	if o >= m.pl {
		return m.ol
	}
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.checks++
		mid := (lo + hi) / 2
		if m.segs[mid].e <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	s := &m.segs[lo]
	return s.a + (o - s.s)
}

func (m *Mapper) ToOut(i int) int {
	m.checks = 0
	if i >= m.ol {
		return m.pl
	}
	lo, hi := 0, len(m.segs)
	for lo < hi {
		m.checks++
		mid := (lo + hi) / 2
		if m.segs[mid].b <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	s := &m.segs[lo]
	if s.b-s.a > s.e-s.s { // deletion: map to its output start
		return s.s
	}
	return s.s + (i - s.a)
}
