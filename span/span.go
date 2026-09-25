// Package span records the correspondence between normalized output offsets
// and original input offsets as an ordered list of non-overlapping output
// ranges. Adjacent kept ranges are coalesced, so the range count grows only
// with the number of deletion points, not with byte count.
package span

// Seg maps output range [OStart,OStart+Len) to original range starting at
// IStart with the same length (Len may be 0: a pure inserted byte).
type Seg struct {
	OStart, IStart int64
	Len            int64
}

func (s Seg) oEnd() int64 { return s.OStart + s.Len }

// Map is a coalesced, sorted segment list; not safe for concurrent use.
type Map struct {
	segs    []Seg
	checked int // segments inspected by the most recent query
}

// Checked returns the segment count inspected by the last ToOrig/ToOut call.
func (m *Map) Checked() int { return m.checked }

// Len returns the number of recorded segments.
func (m *Map) Len() int { return len(m.segs) }

// Seg returns segment i.
func (m *Map) Seg(i int) Seg { return m.segs[i] }

// Add appends a range, coalescing with a directly adjacent prior range.
func (m *Map) Add(oStart, iStart, length int64) {
	n := len(m.segs)
	if length > 0 && n > 0 {
		p := &m.segs[n-1]
		if p.Len > 0 && p.oEnd() == oStart && p.IStart+p.Len == iStart {
			p.Len += length
			return
		}
	}
	m.segs = append(m.segs, Seg{oStart, iStart, length})
}

// finder returns the first seg whose oEnd > o and counts inspected segs.
func (m *Map) finder(o int64) int {
	lo, hi := 0, len(m.segs)
	c := 0
	for lo < hi {
		c++
		mid := (lo + hi) / 2
		if m.segs[mid].oEnd() <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.checked = c
	return lo
}

// ToOrig maps output offset o in [0,outLen] to an original offset.
func (m *Map) ToOrig(o int64) int64 {
	i := m.finder(o)
	switch {
	case i < len(m.segs):
		s := m.segs[i]
		if o >= s.OStart {
			return s.IStart + (o - s.OStart)
		}
		if i > 0 {
			return m.segs[i-1].IStart + m.segs[i-1].Len
		}
		return 0
	case len(m.segs) > 0:
		s := m.segs[len(m.segs)-1]
		return s.IStart + s.Len
	default:
		return 0
	}
}

// ToOut maps original offset i in [0,origLen] to an output offset.
func (m *Map) ToOut(i int64) int64 {
	lo, hi := 0, len(m.segs)
	c := 0
	for lo < hi {
		c++
		mid := (lo + hi) / 2
		if m.segs[mid].IStart+m.segs[mid].Len <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.checked = c
	switch {
	case lo < len(m.segs):
		s := m.segs[lo]
		if i >= s.IStart {
			return s.OStart + (i - s.IStart)
		}
		if lo > 0 {
			return m.segs[lo-1].OStart + m.segs[lo-1].Len
		}
		return 0
	case len(m.segs) > 0:
		s := m.segs[len(m.segs)-1]
		return s.OStart + s.Len
	default:
		return 0
	}
}

// Truncate discards output strictly past oEnd and returns the new map.
func (m *Map) Truncate(oEnd int64) *Map {
	r := &Map{}
	for _, s := range m.segs {
		if s.OStart >= oEnd {
			continue
		}
		if s.oEnd() <= oEnd {
			r.segs = append(r.segs, s)
			continue
		}
		cut := oEnd - s.OStart
		r.segs = append(r.segs, Seg{s.OStart, s.IStart, cut})
	}
	return r
}

// Merge appends every segment of other shifted by (dOut,dOrig), coalescing
// across the join when both sides keep bytes there.
func (m *Map) Merge(other *Map, dOut, dOrig int64) {
	for _, s := range other.segs {
		m.Add(s.OStart+dOut, s.IStart+dOrig, s.Len)
	}
}
