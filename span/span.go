// Package span records which original byte intervals were deleted while
// producing an output byte stream, and answers bidirectional offset
// queries with binary search.
package span

// Del is a deleted original interval [OrigStart, OrigEnd). OutPos is the
// output offset immediately after the deletion point.
type Del struct {
	OrigStart int
	OrigEnd   int
	OutPos    int
}

// Map is a deletion table. Intervals are non-overlapping and ordered by
// OrigStart; adjacent deletions are coalesced.
type Map struct {
	dels    []Del
	outLen  int
	checked int
}

// New returns an empty map for an output of outLen bytes.
func New(outLen int) *Map { return &Map{outLen: outLen} }

// Delete records deletion of original interval [a,b) at output point p
// (the output offset immediately after the deletion). Adjacent trailing
// intervals are coalesced when their source ranges are contiguous.
func (m *Map) Delete(a, b, p int) {
	if a >= b {
		return
	}
	n := len(m.dels)
	if n > 0 && m.dels[n-1].OrigEnd == a {
		m.dels[n-1].OrigEnd = b
		return
	}
	m.dels = append(m.dels, Del{a, b, p})
}

// ReplaceSuffix drops intervals whose source range starts at or after a
// and inserts one interval [a,b) at output point p, merging with a
// contiguous surviving predecessor. Used when the end-of-file policy
// turns previously kept trailing newlines into one big suffix deletion.
func (m *Map) ReplaceSuffix(a, b, p int) {
	idx := len(m.dels)
	for idx > 0 && m.dels[idx-1].OrigStart >= a {
		idx--
	}
	m.dels = m.dels[:idx]
	if idx > 0 && m.dels[idx-1].OrigEnd == a {
		m.dels[idx-1].OrigEnd = b
		return
	}
	m.dels = append(m.dels, Del{a, b, p})
}

// Count returns the number of mapped intervals.
func (m *Map) Count() int { return len(m.dels) }

// Checked returns intervals inspected by the most recent query.
func (m *Map) Checked() int { return m.checked }

// ToOrig maps output offset o in [0,outLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	m.checked = 0
	lo, hi := 0, len(m.dels)
	for lo < hi {
		mid := (lo + hi) / 2
		m.checked++
		if m.dels[mid].OutPos < o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	i := lo
	if i < len(m.dels) && o == m.dels[i].OutPos {
		return m.dels[i].OrigEnd
	}
	if i > 0 {
		d := m.dels[i-1]
		return o + d.OrigEnd - d.OutPos
	}
	return o
}

// ToOut maps original offset i in [0,origLen] to an output offset.
func (m *Map) ToOut(i int) int {
	m.checked = 0
	lo, hi := 0, len(m.dels)
	for lo < hi {
		mid := (lo + hi) / 2
		m.checked++
		if m.dels[mid].OrigEnd < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	idx := lo
	if idx < len(m.dels) && i >= m.dels[idx].OrigStart && i <= m.dels[idx].OrigEnd {
		return m.dels[idx].OutPos
	}
	if idx > 0 {
		d := m.dels[idx-1]
		return i - (d.OrigEnd - d.OutPos)
	}
	return i
}
