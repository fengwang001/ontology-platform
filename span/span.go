// Package span stores a piecewise bidirectional original<->output offset map.
// Segments are half-open: [A0,A1) in original <-> [B0,B1) in output.
package span

// Seg is one correspondence: identity, deletion (B0==B1), insertion (A0==A1).
type Seg struct{ A0, A1, B0, B1 int }

// Map is an ordered, adjacent-identity-merged segment table.
type Map struct {
	s               []Seg
	probes          int
	origLen, outLen int
}

// Add appends one segment, merging with a contiguous identity predecessor.
func (m *Map) Add(a0, a1, b0, b1 int) {
	if a0 == a1 && b0 == b1 {
		return
	}
	m.origLen, m.outLen = a1, b1
	if n := len(m.s); n > 0 {
		p := m.s[n-1]
		if a1-a0 == b1-b0 && p.A1-p.A0 == p.B1-p.B0 && p.A1 == a0 && p.B1 == b0 {
			m.s[n-1].A1, m.s[n-1].B1 = a1, b1
			return
		}
	}
	m.s = append(m.s, Seg{a0, a1, b0, b1})
}

func (m *Map) Seqs() []Seg       { return m.s }
func (m *Map) Len() int          { return len(m.s) }
func (m *Map) Probes() int       { return m.probes }
func (m *Map) Sizes() (int, int) { return m.origLen, m.outLen }

// Finish declares final stream sizes for endpoint queries.
func (m *Map) Finish(orig, out int) { m.origLen, m.outLen = orig, out }

// ToOrig maps output offset o in [0,outLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	m.probes = 0
	lo, n := 0, len(m.s)
	hi := n
	for lo < hi {
		m.probes++
		mid := (lo + hi) / 2
		if m.s[mid].B1 <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.probes++
	if lo >= n {
		return m.origLen
	}
	g := m.s[lo]
	if o < g.B0 { // deletion gap: no output offset lands inside
		if lo > 0 {
			return m.s[lo-1].A1
		}
		return g.A0
	}
	if g.A1 == g.A0 {
		return g.A0 // synthetic insertion collapses to its original edge
	}
	return g.A0 + (o - g.B0)
}

// ToOut maps original offset i in [0,origLen] to an output offset;
// deleted runs use their right edge so the cursor lands after them.
func (m *Map) ToOut(i int) int {
	m.probes = 0
	lo, n := 0, len(m.s)
	hi := n
	for lo < hi {
		m.probes++
		mid := (lo + hi) / 2
		if m.s[mid].A1 <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.probes++
	if lo >= n {
		return m.outLen
	}
	g := m.s[lo]
	if i < g.A0 {
		if lo > 0 {
			return m.s[lo-1].B1
		}
		return g.B0
	}
	if g.B1 == g.B0 {
		return g.B1
	}
	if g.A1 == g.A0 {
		return g.B1
	}
	return g.B0 + (i - g.A0)
}

// Translate returns a shifted copy (for parallel stitching).
func (m *Map) Translate(da, db int) Map {
	c := Map{s: make([]Seg, len(m.s)), origLen: m.origLen + da, outLen: m.outLen + db}
	for i, g := range m.s {
		c.s[i] = Seg{g.A0 + da, g.A1 + da, g.B0 + db, g.B1 + db}
	}
	return c
}

// Merge appends all segments of other, coalescing across the seam.
func (m *Map) Merge(other *Map) {
	for _, g := range other.s {
		m.Add(g.A0, g.A1, g.B0, g.B1)
	}
	m.origLen, m.outLen = other.origLen, other.outLen
}

// FoldTail keeps only the last k trailing newlines and turns all bytes after
// them (extra newlines, deleted whitespace) into deletions. nlOut/nlOrig are
// parallel trailing-newline positions, newest last; orig -1 = synthetic.
func (m *Map) FoldTail(k int, nlOrig, nlOut []int) int {
	origEnd, outEnd := m.origLen, m.outLen
	if k >= len(nlOut) {
		return outEnd
	}
	idx := k // keep the earliest k newlines; idx is the first dropped one
	cutB := nlOut[idx] // first retained newline (or first dropped when k==0)
	var kept []Seg
	for _, g := range m.s {
		if g.B1 <= cutB {
			kept = append(kept, g)
			continue
		}
		if g.B0 < cutB && g.A1-g.A0 == g.B1-g.B0 && g.A1 > g.A0 {
			g.A1, g.B1 = g.A0+(cutB-g.B0), cutB
			kept = append(kept, g)
		}
		break
	}
	m.s = kept
	m.Finish(origEnd, cutB)
	return cutB
}
