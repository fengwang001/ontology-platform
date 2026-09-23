// Package span records correspondences between original and normalized byte
// offsets and answers bidirectional queries by binary search.
package span

import "sort"

// seg maps a half-open original range to a half-open output range.
// Deleted bytes are represented with ol==oe (a zero-length output range).
type seg struct {
	ol, or int // original [ol, or)
	nl, ne int // output [nl, ne)
}

// Map is an append-only correspondence table. Only deletion points create
// segments; a stream with no deletions collapses to a single identity segment.
type Map struct {
	s       []seg
	probes  int // segments inspected by the most recent query
	lastOL  int
	lastNL  int
}

// Keep records that n original bytes are emitted unchanged at both ends.
func (m *Map) Keep(n int) {
	if n <= 0 {
		return
	}
	if len(m.s) > 0 {
		last := &m.s[len(m.s)-1]
		if last.or == m.lastOL && last.ne == m.lastNL && last.or-last.ol == last.ne-last.nl {
			last.or += n
			last.ne += n
			m.lastOL += n
			m.lastNL += n
			return
		}
	}
	m.s = append(m.s, seg{m.lastOL, m.lastOL + n, m.lastNL, m.lastNL + n})
	m.lastOL += n
	m.lastNL += n
}

// Drop records that n original bytes were deleted (no output produced).
func (m *Map) Drop(n int) {
	if n <= 0 {
		return
	}
	if len(m.s) > 0 && m.s[len(m.s)-1].or == m.lastOL && m.s[len(m.s)-1].ne == m.lastNL {
		last := &m.s[len(m.s)-1]
		last.or += n
		m.lastOL += n
		return
	}
	m.s = append(m.s, seg{m.lastOL, m.lastOL + n, m.lastNL, m.lastNL})
	m.lastOL += n
}

// Segments reports how many mapping segments are stored.
func (m *Map) Segments() int { return len(m.s) }

// Probes reports segments inspected by the latest ToOrig/ToOut call.
func (m *Map) Probes() int { return m.probes }

// ToOrig maps an output offset o in [0, OutLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	i, cnt := sort.Search(len(m.s), func(i int) bool {
		return m.s[i].ne > o
	}), 0
	if i < len(m.s) {
		cnt++
		if o >= m.s[i].nl {
			m.probes = cnt
			return m.s[i].ol + (o - m.s[i].nl)
		}
	}
	// o lies in an output gap: map to the start of the next kept original byte.
	if i < len(m.s) {
		m.probes = cnt
		return m.s[i].ol
	}
	m.probes = cnt
	return m.lastOL
}

// ToOut maps an original offset i in [0, OrigLen] to an output offset.
// Bytes inside a deleted run collapse onto the following output point.
func (m *Map) ToOut(i int) int {
	j := sort.Search(len(m.s), func(k int) bool {
		cnt := m.probes + 1
		m.probes = cnt
		return m.s[k].or > i
	})
	if j < len(m.s) && i >= m.s[j].ol {
		if m.s[j].ne > m.s[j].nl { // kept segment
			return m.s[j].nl + (i - m.s[j].ol)
		}
	}
	// deleted run (or trailing end): collapse to the next output boundary.
	if j < len(m.s) {
		return m.s[j].ne
	}
	return m.lastNL
}

// OrigLen is the total original byte count recorded.
func (m *Map) OrigLen() int { return m.lastOL }

// OutLen is the total output byte count recorded.
func (m *Map) OutLen() int { return m.lastNL }

// Merge appends the segments of o shifted by (origShift, outShift), coalescing
// adjacent identity segments. Used to stitch parallel chunks.
func (m *Map) Merge(o *Map, origShift, outShift int) {
	for _, e := range o.s {
		m.s = append(m.s, seg{e.ol + origShift, e.or + origShift, e.nl + outShift, e.ne + outShift})
	}
	m.lastOL = origShift + o.lastOL
	m.lastNL = outShift + o.lastNL
	m.coalesce()
}

func (m *Map) coalesce() {
	out := m.s[:0]
	for _, e := range m.s {
		if n := len(out); n > 0 {
			p := &out[n-1]
			if p.or == e.ol && p.ne == e.nl &&
				(e.or-e.ol == e.ne-e.nl) && (p.or-p.ol == p.ne-p.nl) {
				p.or, p.ne = e.or, e.ne
				continue
			}
		}
		out = append(out, e)
	}
	m.s = out
}
