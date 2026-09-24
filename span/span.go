// Package span records correspondences between original byte ranges and
// output byte ranges and answers bidirectional offset queries by binary
// search. Deleted bytes are gaps between spans; only surviving content is
// stored, so the number of spans grows with deletion points, not with size.
package span

// Span maps [Orig0, Orig0+Len) to [Out0, Out0+Len); Len may be 0 only for a
// synthesized trailing newline (a point alias at the end of the original).
type Span struct {
	Orig0 int
	Out0  int
	Len   int
}

func (s Span) origEnd() int { return s.Orig0 + s.Len }
func (s Span) outEnd() int  { return s.Out0 + s.Len }

// Map is an ordered, disjoint list of spans. Zero value is an empty map.
type Map struct {
	s          []Span
	lastChecks int // intervals examined by the most recent query
}

// Count returns the number of stored intervals.
func (m *Map) Count() int { return len(m.s) }

// LastChecks returns intervals examined by the most recent ToOrig/ToOut call.
func (m *Map) LastChecks() int { return m.lastChecks }

// Add appends a span, coalescing with the previous one when both are ordinary
// 1:1 ranges that are contiguous on both axes (keeps span count bounded).
func (m *Map) Add(orig0, out0, length int) {
	if n := len(m.s); n > 0 {
		p := m.s[n-1]
		if p.Len > 0 && length > 0 && p.origEnd() == orig0 && p.outEnd() == out0 {
			m.s[n-1].Len += length
			return
		}
	}
	m.s = append(m.s, Span{orig0, out0, length})
}

// Cut truncates the output (and corresponding originals) to outLen bytes.
func (m *Map) Cut(outLen int) {
	kept := m.s[:0]
	for _, sp := range m.s {
		switch {
		case sp.outEnd() <= outLen:
			kept = append(kept, sp)
		case sp.Out0 >= outLen:
			// drop
		default:
			kept = append(kept, Span{sp.Orig0, sp.Out0, outLen - sp.Out0})
		}
	}
	m.s = kept
}

// ToOrig maps an output offset in [0,outLen] to an original offset. Output
// positions inside a span map 1:1; positions at a gap map to the next live
// original byte (so a surviving byte round-trips), with the trailing edge
// preferred at exact span ends.
func (m *Map) ToOrig(o int) int {
	lo, hi := 0, len(m.s)
	for lo < hi {
		mid := (lo + hi) / 2
		if m.s[mid].outEnd() <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = 1
	for k := lo; k > 0; k >>= 1 {
		m.lastChecks++
	}
	if lo == len(m.s) {
		if len(m.s) > 0 {
			return m.s[len(m.s)-1].origEnd()
		}
		return 0
	}
	sp := m.s[lo]
	if o <= sp.Out0 {
		return sp.Orig0
	}
	return sp.Orig0 + (o - sp.Out0)
}

// ToOut maps an original offset in [0,origLen] to an output offset. Offsets
// inside a deleted gap collapse to the output position just before the gap
// (cursor lands before the line's newline); a final zero-length synthesized
// span aliases the original end to its output position.
func (m *Map) ToOut(i int) int {
	lo, hi := 0, len(m.s)
	for lo < hi {
		mid := (lo + hi) / 2
		if m.s[mid].Orig0 < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = 1
	for k := lo; k > 0; k >>= 1 {
		m.lastChecks++
}
	if lo == len(m.s) {
		if len(m.s) > 0 {
			return m.s[len(m.s)-1].outEnd()
		}
		return 0
	}
	sp := m.s[lo]
	if i < sp.Orig0 { // start of a gap immediately before this span
		if lo > 0 {
			return m.s[lo-1].outEnd()
		}
		return 0
	}
	if sp.Len == 0 { // synthesized newline: alias its output point
		return sp.Out0
	}
	if i <= sp.origEnd() {
		return sp.Out0 + (i - sp.Orig0)
	}
	return sp.outEnd()
}
