// Package span records correspondences between original byte intervals and
// output byte intervals and answers both-direction offset queries by binary
// search. Only intervals that differ from an identity (1:1) interval are
// stored: deletions (output length 0) and insertions (original length 0).
// Adjacent identity intervals are implicit, so the number of stored
// intervals grows with the number of deletion/insertion points, not with
// the number of output bytes.
package span

// Seg is one mapping interval, half-open on both axes.
// OLen==0 means a deletion; OStart==OEnd then.
// OEnd-OStart != 0 && OEnd-OStart==OLen means identity (only stored when the
// builder needs to anchor a non-identity interval).
// ILen==0 means an insertion (OStart<OEnd, IStart==IEnd).
type Seg struct {
	IStart, IEnd int
	OStart, OEnd int
}

func (s Seg) ilen() int { return s.IEnd - s.IStart }
func (s Seg) olen() int { return s.OEnd - s.OStart }

// Builder accumulates segments in increasing original order.
type Builder struct {
	segs []Seg
}

// Add appends one interval. Added intervals must tile [0,origLen) and
// [0,outLen) with no gaps or overlaps.
func (b *Builder) Add(s Seg) {
	if n := len(b.segs); n > 0 {
		p := b.segs[n-1]
		if p.IEnd == s.IStart && p.OEnd == s.OStart &&
			p.ilen() == p.olen() && s.ilen() == s.olen() {
			b.segs[n-1].IEnd, b.segs[n-1].OEnd = s.IEnd, s.OEnd
			return
		}
	}
	b.segs = append(b.segs, s)
}

// Segs returns the stored intervals.
func (b *Builder) Segs() []Seg { return b.segs }

// Build freezes the intervals into a queryable map with total axis lengths.
func (b *Builder) Build(origLen, outLen int) *Map {
	return &Map{segs: b.segs, ilen: origLen, olen: outLen}
}

// Map answers offset queries. A Map is safe for concurrent queries.
type Map struct {
	segs       []Seg
	ilen, olen int
	lastChecks int
}

// Checks reports the number of intervals inspected by the most recent
// ToOrig/ToOut query (binary-search probe count), for complexity tests.
func (m *Map) Checks() int { return m.lastChecks }

// Count returns the number of stored intervals.
func (m *Map) Count() int { return len(m.segs) }

// ToOrig maps output offset o (0<=o<=OutLen) to an original offset.
func (m *Map) ToOrig(o int) int {
	lo, hi, probes := 0, len(m.segs), 0
	for lo < hi {
		probes++
		mid := (lo + hi) / 2
		if m.segs[mid].OEnd <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = probes
	if lo < len(m.segs) && o >= m.segs[lo].OStart {
		s := m.segs[lo]
		if o == s.OStart {
			return s.IStart // shared boundary: keep round-trip ToOut->this o
		}
		if s.olen() == 0 {
			return s.IEnd
		}
		return s.IStart + (o - s.OStart)
	}
	if lo > 0 {
		p := m.segs[lo-1]
		return o - (p.OEnd - p.IEnd)
	}
	return o
}

// ToOut maps original offset i (0<=i<=OrigLen) to an output offset. Bytes in
// a deleted interval map to the output position just before the interval.
func (m *Map) ToOut(i int) int {
	lo, hi, probes := 0, len(m.segs), 0
	for lo < hi {
		probes++
		mid := (lo + hi) / 2
		if m.segs[mid].IEnd <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = probes
	idx := lo
	if idx > 0 {
		s := m.segs[idx-1]
		if i == s.IStart && s.ilen() == 0 {
			// shared boundary: prefer the segment starting here (insertion)
		} else if i >= s.IStart {
			if s.ilen() == 0 {
				return s.OStart
			}
			return s.OStart + (i - s.IStart)
		}
	}
	if idx < len(m.segs) && i == m.segs[idx].IStart && m.segs[idx].ilen() == 0 {
		return m.segs[idx].OStart
	}
	if idx > 0 {
		p := m.segs[idx-1]
		return i - (p.IEnd - p.OEnd)
	}
	return i
}

// Lengths returns the total original and output lengths.
func (m *Map) Lengths() (orig, out int) { return m.ilen, m.olen }
