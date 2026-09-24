// Package span stores bidirectional byte-offset correspondence between an
// original buffer and normalized output, as coalesced monotone runs.
package span

// Run maps original [O0,O1) to output [N0,N1); zero length on one side
// marks a deletion (output length 0) or insertion (original length 0).
type Run struct {
	O0, O1, N0, N1 int
}

// Mapper is append-only except Truncate at a run boundary (end rewrites).
type Mapper struct {
	runs []Run
	oEnd int
	nEnd int
	last int // intervals examined by the most recent query
}

func (m *Mapper) add(r Run) {
	if k := len(m.runs); k > 0 {
		p := m.runs[k-1]
		if p.O1 == r.O0 && p.N1 == r.N0 && p.N0-p.O0 == r.N0-r.O0 {
			m.runs[k-1].O1, m.runs[k-1].N1 = r.O1, r.N1
			m.oEnd, m.nEnd = r.O1, r.N1
			return
		}
	}
	m.runs = append(m.runs, r)
	m.oEnd, m.nEnd = r.O1, r.N1
}

// Emit records n copied bytes (identity, offset by prior deletions).
func (m *Mapper) Emit(n int) {
	if n > 0 {
		m.add(Run{m.oEnd, m.oEnd + n, m.nEnd, m.nEnd + n})
	}
}

// Delete records oLen original bytes with no output (cursor stays).
func (m *Mapper) Delete(oLen int) {
	if oLen > 0 {
		m.add(Run{m.oEnd, m.oEnd + oLen, m.nEnd, m.nEnd})
	}
}

// Insert records n output bytes synthesized (no original counterpart).
func (m *Mapper) Insert(n int) {
	if n > 0 {
		m.add(Run{m.oEnd, m.oEnd, m.nEnd, m.nEnd + n})
	}
}

// Truncate rewinds to original boundary o; n must be its output boundary.
func (m *Mapper) Truncate(o, n int) {
	for len(m.runs) > 0 && m.runs[len(m.runs)-1].O0 >= o {
		m.runs = m.runs[:len(m.runs)-1]
	}
	m.oEnd, m.nEnd = o, n
	if k := len(m.runs); k > 0 {
		m.runs[k-1].O1, m.runs[k-1].N1 = o, n
	}
}

// Runs returns the coalesced mapping intervals (copy).
func (m *Mapper) Runs() []Run { return append([]Run(nil), m.runs...) }

// OrigLen and OutLen are current cursor positions.
func (m *Mapper) OrigLen() int { return m.oEnd }
func (m *Mapper) OutLen() int  { return m.nEnd }

// ToOrig maps output offset o (0..OutLen) to an original offset.
func (m *Mapper) ToOrig(o int) int {
	m.last = 0
	lo, hi := 0, len(m.runs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		m.last++
		if m.runs[mid].N1 < o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	r := m.runs[lo]
	return r.O0 + (o - r.N0)
}

// ToOut maps original offset i (0..OrigLen) to an output offset.
func (m *Mapper) ToOut(i int) int {
	m.last = 0
	lo, hi := 0, len(m.runs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		m.last++
		if m.runs[mid].O1 < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	r := m.runs[lo]
	return r.N0 + (i - r.O0)
}

// LastChecks reports intervals examined by the latest ToOrig/ToOut call.
func (m *Mapper) LastChecks() int { return m.last }
