// Package span records the correspondence between original byte ranges and
// output byte ranges as piecewise-linear segments and answers bidirectional
// offset queries by binary search.
package span

// Seg is one half-open mapping [A0,A1) -> [B0,B1), with linear interpolation.
// A deletion is a segment with B0 == B1.
type Seg struct {
	A0, A1 int
	B0, B1 int
}

// Map is an ordered list of non-overlapping, abutting segments.
type Map struct {
	segs []Seg
	aEnd int
	bEnd int
	// probes counts the segment boundary checks made by the most recent
	// ToOrig/ToOut query.
	probes int
}

// New returns an empty map.
func New() *Map { return &Map{} }

// Add appends a segment, coalescing it with an identity predecessor (two
// adjacent identity segments are indistinguishable, which keeps the segment
// count proportional only to deletion points).
func (m *Map) Add(s Seg) {
	if n := len(m.segs); n > 0 {
		p := m.segs[n-1]
		if p.A1 == s.A0 && p.B1 == s.B0 && p.A1-p.A0 == p.B1-p.B0 &&
			s.A1-s.A0 == s.B1-s.B0 {
			m.segs[n-1].A1, m.segs[n-1].B1 = s.A1, s.B1
			return
		}
	}
	m.segs = append(m.segs, s)
}

// ExtendLast grows the original extent of the final segment by n bytes with
// no growth on the output side (used to fold a "\r\n" pair into one segment).
func (m *Map) ExtendLast(n int) { m.segs[len(m.segs)-1].A1 += n }

// Finish declares the total original and output lengths.
func (m *Map) Finish(aEnd, bEnd int) { m.aEnd, m.bEnd = aEnd, bEnd }

// Segs returns the underlying segments for stitching.
func (m *Map) Segs() []Seg { return m.segs }

// Ends reports the total original and output byte counts.
func (m *Map) Ends() (int, int) { return m.aEnd, m.bEnd }

// Probes reports the boundary checks performed by the last bidirectional query.
func (m *Map) Probes() int { return m.probes }

// firstAfter returns the index of the first segment whose extent on the
// chosen axis ends strictly after p, using a manual binary search.
func (m *Map) firstAfter(p int, out bool) int {
	lo, hi := 0, len(m.segs)
	m.probes = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.probes++
		end := m.segs[mid].A1
		if out {
			end = m.segs[mid].B1
		}
		if end <= p {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// ToOrig maps an output offset o in [0, bEnd] to an original offset.
func (m *Map) ToOrig(o int) int {
	i := m.firstAfter(o, true)
	if i == len(m.segs) {
		return m.aEnd
	}
	s := m.segs[i]
	if s.B1-s.B0 == 0 {
		return s.A1
	}
	return s.A0 + (o-s.B0)*(s.A1-s.A0)/(s.B1-s.B0)
}

// ToOut maps an original offset i in [0, aEnd] to an output offset. Offsets
// inside a deleted run resolve to the run's output endpoint (end aligned).
func (m *Map) ToOut(i int) int {
	j := m.firstAfter(i, false)
	if j == len(m.segs) {
		return m.bEnd
	}
	s := m.segs[j]
	if s.A1-s.A0 == 0 {
		return s.B1
	}
	if s.B1-s.B0 == 0 {
		return s.B1
	}
	return s.B0 + (i-s.A0)*(s.B1-s.B0)/(s.A1-s.A0)
}

// Truncate drops every output byte at or after q and the corresponding
// original extent, clamping the final crossing segment.
func (m *Map) Truncate(q int) {
	i := m.firstAfter(q, true)
	m.segs = m.segs[:i]
	for k := len(m.segs) - 1; k >= 0; k-- {
		s := &m.segs[k]
		if s.B0 >= q {
			m.segs = m.segs[:k]
			continue
		}
		if s.B1 > q {
			d := q - s.B0
			s.B1 = q
			if s.A1-s.A0 == s.B1-s.B0 && d >= 0 {
				s.A1 = s.A0 + d
			} else {
				s.A1 = s.A0 + d*(s.A1-s.A0)/(s.B1-s.B0)
			}
		}
		break
	}
	m.bEnd = q
	if n := len(m.segs); n > 0 {
		m.aEnd = m.segs[n-1].A1
	}
}
