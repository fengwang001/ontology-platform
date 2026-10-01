// Package span records which input byte ranges were deleted from the
// output and answers bidirectional offset queries in O(log n) by
// binary search. Only deletions (and a trailing appended byte count)
// are stored, so the table grows with the number of deletion points,
// not with the output size. A Map is not safe for concurrent use.
package span

// rec describes deleted input bytes [orig, orig+del) that sit at
// output offset out. recs are sorted by orig and by out.
type rec struct{ orig, out, del int }

// Map is a bidirectional offset mapping between input and output.
type Map struct {
	Orig     int // total input bytes (set by the producer)
	Out      int // total output bytes (set by the producer)
	Appended int // output bytes appended past the input end
	recs     []rec
	pref     []int // pref[i] = total deleted bytes in recs[:i+1]
	del      int
	probe    int // intervals inspected by the last ToOrig/ToOut
}

// AddDelete records that input [orig, orig+del) was deleted while the
// output was at offset out. Calls must arrive with non-decreasing
// orig; runs contiguous in both coordinates are merged.
func (m *Map) AddDelete(orig, del, out int) {
	if del <= 0 {
		return
	}
	if n := len(m.recs); n > 0 {
		if last := &m.recs[n-1]; last.orig+last.del == orig && last.out == out {
			last.del += del
			m.pref[n-1] += del
			m.del += del
			return
		}
	}
	m.recs = append(m.recs, rec{orig, out, del})
	m.pref = append(m.pref, m.del+del)
	m.del += del
}

// Append merges other's records into m, shifted by the given bases.
func (m *Map) Append(o *Map, origBase, outBase int) {
	for _, r := range o.recs {
		m.AddDelete(r.orig+origBase, r.del, r.out+outBase)
	}
	m.Appended += o.Appended
}

// Recs returns the number of stored intervals.
func (m *Map) Recs() int { return len(m.recs) }

// Probe returns how many intervals the last query inspected.
func (m *Map) Probe() int { return m.probe }

// search counts recs whose [orig,orig+del) ends at or before i.
func (m *Map) searchEnd(i int) int {
	lo, hi := 0, len(m.recs)
	for lo < hi {
		m.probe++
		if mid := (lo + hi) / 2; m.recs[mid].orig+m.recs[mid].del <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// searchOut counts recs whose output offset is at or before o.
func (m *Map) searchOut(o int) int {
	lo, hi := 0, len(m.recs)
	for lo < hi {
		m.probe++
		if mid := (lo + hi) / 2; m.recs[mid].out <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// ToOut maps an input offset in [0, Orig] to an output offset.
// Deleted bytes map to the position of the next surviving byte.
func (m *Map) ToOut(i int) int {
	m.probe = 0
	k := m.searchEnd(i)
	d := 0
	if k > 0 {
		d = m.pref[k-1]
	}
	if k < len(m.recs) && m.recs[k].orig < i {
		m.probe++
		d += i - m.recs[k].orig
	}
	return i - d
}

// ToOrig maps an output offset in [0, Out] back to an input offset.
func (m *Map) ToOrig(o int) int {
	m.probe = 0
	k := m.searchOut(o)
	if k > 0 {
		o += m.pref[k-1]
	}
	if o > m.Orig {
		o = m.Orig
	}
	return o
}
