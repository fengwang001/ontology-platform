// Package span records a piecewise, bidirectional mapping between original
// byte offsets and normalized output byte offsets. Intervals are half-open.
package span

// Seg is one affine piece: original [OrigStart, OrigEnd) maps to output
// [OutStart, OutStart+OutLen). OutLen==0 is a deletion; OrigEnd==OrigStart
// with OutLen>0 is an insertion; otherwise lengths must be equal (a copy).
type Seg struct {
	OrigStart int
	OrigEnd   int
	OutStart  int
	OutLen    int
}

func (s Seg) origLen() int { return s.OrigEnd - s.OrigStart }
func (s Seg) outEnd() int  { return s.OutStart + s.OutLen }

// Map is an ordered, gapless sequence of segments covering [0, inLen) in the
// original and [0, outLen) in the output (insertions add output-only points).
type Map struct {
	segs      []Seg
	inLen     int
	outLen    int
	lastCheck int
}

// New returns an empty map.
func New() *Map { return &Map{} }

// Append records that original [origStart, origEnd) (which must continue the
// current coverage) produced outLen output bytes.
func (m *Map) Append(origStart, origEnd, outLen int) {
	if len(m.segs) > 0 {
		last := &m.segs[len(m.segs)-1]
		lastDel := last.OutLen == 0
		del := outLen == 0
		if last.OrigEnd == origStart && last.outEnd() == m.outLen && lastDel == del {
			last.OrigEnd = origEnd
			last.OutLen += outLen
			m.inLen = origEnd
			m.outLen += outLen
			return
		}
	}
	m.segs = append(m.segs, Seg{origStart, origEnd, m.outLen, outLen})
	m.inLen = origEnd
	m.outLen += outLen
}

// Merge appends all segments of o shifted by original base ao and output base
// bo, coalescing abutting pieces of the same kind.
func (m *Map) Merge(o *Map, ao, bo int) {
	for _, s := range o.segs {
		m.appendRaw(Seg{s.OrigStart + ao, s.OrigEnd + ao, s.OutStart + bo, s.OutLen})
	}
}

func (m *Map) appendRaw(s Seg) {
	if len(m.segs) > 0 {
		last := &m.segs[len(m.segs)-1]
		if last.OrigEnd == s.OrigStart && last.outEnd() == s.OutStart &&
			(last.OutLen == 0) == (s.OutLen == 0) {
			last.OrigEnd = s.OrigEnd
			last.OutLen += s.OutLen
			m.inLen = s.OrigEnd
			m.outLen = s.outEnd()
			return
		}
	}
	m.segs = append(m.segs, s)
	if s.OrigEnd > m.inLen {
		m.inLen = s.OrigEnd
	}
	if s.outEnd() > m.outLen {
		m.outLen = s.outEnd()
	}
}

// Truncate drops every decision strictly after (origAt, outAt) and cuts a
// segment straddling the point. Used for final newline surgery.
func (m *Map) Truncate(origAt, outAt int) {
	kept := m.segs[:0]
	for _, s := range m.segs {
		switch {
		case s.OrigEnd <= origAt && s.outEnd() <= outAt:
			kept = append(kept, s)
		case s.OrigStart >= origAt || s.OutStart >= outAt:
			// drop entirely
		default:
			c := origAt - s.OrigStart
			if c2 := outAt - s.OutStart; c2 < c {
				c = c2
			}
			if c > 0 {
				s.OrigEnd = s.OrigStart + c
				s.OutLen = c
				kept = append(kept, s)
			}
		}
	}
	m.segs = kept
	m.inLen = origAt
	m.outLen = outAt
}

// Segs returns the recorded segments.
func (m *Map) Segs() []Seg { return m.segs }

// InLen is the covered original length, OutLen the produced output length.
func (m *Map) InLen() int  { return m.inLen }
func (m *Map) OutLen() int { return m.outLen }

// LastCheck reports how many segments the most recent query inspected.
func (m *Map) LastCheck() int { return m.lastCheck }

// ToOrig maps output offset o (0..OutLen) to an original offset.
func (m *Map) ToOrig(o int) int {
	lo, hi := 0, len(m.segs)
	m.lastCheck = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastCheck++
		if m.segs[mid].OutStart < o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	// lo is first seg with OutStart>=o; candidate is lo (at boundary) or lo-1.
	if lo < len(m.segs) && m.segs[lo].OutStart == o {
		return m.segs[lo].OrigStart
	}
	s := m.segs[lo-1]
	d := o - s.OutStart
	if d > s.origLen() {
		d = s.origLen()
	}
	return s.OrigStart + d
}

// ToOut maps original offset i (0..InLen) to an output offset.
func (m *Map) ToOut(i int) int {
	lo, hi := 0, len(m.segs)
	m.lastCheck = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastCheck++
		if m.segs[mid].OrigStart < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(m.segs) && m.segs[lo].OrigStart == i {
		return m.segs[lo].OutStart
	}
	s := m.segs[lo-1]
	d := i - s.OrigStart
	if s.OutLen == 0 || d > s.origLen() {
		return s.OutStart
	}
	return s.OutStart + d
}
