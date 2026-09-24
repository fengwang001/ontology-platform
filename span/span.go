// Package span records the correspondence between original and normalized
// byte offsets and answers bidirectional queries.
//
// Only byte-for-byte copied ranges are stored, as equal-length segments
// mapping output [o0,o1) to original [i0,i1). Deleted bytes are simply
// absent: consecutive segments differ by a deletion gap on either axis.
// Segment count therefore grows with the number of deletion points, not
// with the number of bytes, and all queries are binary searches.
package span

type seg struct {
	o0, o1 int
	i0, i1 int
}

// Mapper is an append-only offset map. The zero value is not usable;
// create one with New.
type Mapper struct {
	s    []seg
	iLen int
	oLen int
	hits int
}

// New returns an empty mapper.
func New() *Mapper { return &Mapper{} }

// OrigLen is the number of original bytes recorded.
func (m *Mapper) OrigLen() int { return m.iLen }

// OutLen is the number of output bytes recorded.
func (m *Mapper) OutLen() int { return m.oLen }

// Segments reports how many copied ranges are stored.
func (m *Mapper) Segments() int { return len(m.s) }

// LastChecks reports how many segments were inspected by the most recent
// ToOrig/ToOut query.
func (m *Mapper) LastChecks() int { return m.hits }

// Copy records n bytes copied verbatim from the current original offset
// to the current output offset.
func (m *Mapper) Copy(n int) {
	if n <= 0 {
		return
	}
	if k := len(m.s) - 1; k >= 0 && m.s[k].i1 == m.iLen && m.s[k].o1 == m.oLen {
		m.s[k].i1 += n
		m.s[k].o1 += n
	} else {
		m.s = append(m.s, seg{m.oLen, m.oLen + n, m.iLen, m.iLen + n})
	}
	m.iLen += n
	m.oLen += n
}

// Delete records n original bytes that produce no output.
func (m *Mapper) Delete(n int) {
	if n > 0 {
		m.iLen += n
	}
}

// PopToOut truncates the recorded output to length o, turning the copied
// bytes beyond o into a trailing original deletion gap.
func (m *Mapper) PopToOut(o int) {
	for len(m.s) > 0 && m.s[len(m.s)-1].o0 >= o {
		m.s = m.s[:len(m.s)-1]
	}
	if k := len(m.s) - 1; k >= 0 && m.s[k].o1 > o {
		m.s[k].o1 = o
		m.s[k].i1 = m.s[k].i0 + (o - m.s[k].o0)
	}
	m.oLen = o
}

// Import appends every segment of src translated by (do, dout), merging
// with the tail when both axes stay contiguous.
func (m *Mapper) Import(src *Mapper, do, dout int) {
	for _, t := range src.s {
		cur := seg{t.o0 + dout, t.o1 + dout, t.i0 + do, t.i1 + do}
		if k := len(m.s) - 1; k >= 0 && m.s[k].i1 == cur.i0 && m.s[k].o1 == cur.o0 {
			m.s[k].i1, m.s[k].o1 = cur.i1, cur.o1
		} else {
			m.s = append(m.s, cur)
		}
	}
	m.iLen = do + src.iLen
	m.oLen = dout + src.oLen
}

// ImportAfter imports src translated by (do,dout), skipping src segments
// whose output start is below skipOut. Used when a prefix of src is
// carry bytes already owned by a preceding region.
func (m *Mapper) ImportAfter(src *Mapper, do, dout, skipOut int) {
	for _, t := range src.s {
		if t.o1 <= skipOut {
			continue
		}
		o0, i0 := t.o0, t.i0
		if o0 < skipOut {
			d := skipOut - o0
			o0 += d
			i0 += d
		}
		cur := seg{o0 + dout, t.o1 + dout, i0 + do, t.i1 + do}
		if k := len(m.s) - 1; k >= 0 && m.s[k].i1 == cur.i0 && m.s[k].o1 == cur.o0 {
			m.s[k].i1, m.s[k].o1 = cur.i1, cur.o1
		} else {
			m.s = append(m.s, cur)
		}
	}
	if src.iLen+do > m.iLen {
		m.iLen = src.iLen + do
	}
	if src.oLen+dout > m.oLen {
		m.oLen = src.oLen + dout
	}
}

// ToOrig maps an output offset in [0,OutLen] to an original offset.
func (m *Mapper) ToOrig(o int) int {
	if o >= m.oLen {
		m.hits = 0
		return m.iLen
	}
	lo, hi, hits := 0, len(m.s), 0
	for lo < hi {
		mid := (lo + hi) / 2
		hits++
		if m.s[mid].o0 <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	k := lo - 1
	m.hits = hits
	return m.s[k].i0 + (o - m.s[k].o0)
}

// ToOut maps an original offset in [0,OrigLen] to an output offset.
// Bytes inside a deletion gap map to the gap's right boundary.
func (m *Mapper) ToOut(i int) int {
	if i >= m.iLen {
		m.hits = 0
		return m.oLen
	}
	lo, hi, hits := 0, len(m.s), 0
	for lo < hi {
		mid := (lo + hi) / 2
		hits++
		if m.s[mid].i1 <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.hits = hits
	if lo == len(m.s) {
		return m.oLen
	}
	t := m.s[lo]
	if i >= t.i0 {
		return t.o0 + (i - t.i0)
	}
	return t.o0
}
