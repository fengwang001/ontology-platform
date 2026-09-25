// Package span maps normalized output offsets to source offsets and back.
package span

import "sort"

// Entries are non-identity ordered intervals. Zero-length kind is an insertion.
type Entries []Entry

// Entry describes one deletion or inserted endpoint.
type Entry struct {
	Kind   int
	Orig   [2]int
	Output [2]int
}

const (
	// Copy is retained content; adjacent copies are coalesced.
	Copy = iota
	// Delete has output length zero.
	Delete
	// Insert has original length zero.
	Insert
)

// Map is a compact coordinate map.
type Map struct {
	entries Entries
	checked int
}

// Part is a contiguous map with both coordinate lengths.
type Part struct {
	Map    *Map
	Orig   int
	Output int
}

// NewMap returns an empty map.
func NewMap() *Map { return &Map{} }

// Entries exposes a copy of non-identity intervals.
func (m *Map) Entries() Entries { return append(Entries(nil), m.entries...) }

// Checked returns intervals inspected by the latest query.
func (m *Map) Checked() int { return m.checked }

// Add appends one interval, coalescing neighboring retained copies.
func (m *Map) Add(kind int, origStart, origEnd, outStart, outEnd int) {
	if origEnd == origStart && outEnd == outStart {
		return
	}
	if n := len(m.entries); n > 0 {
		last := &m.entries[n-1]
		sameCopy := kind == Copy && last.Kind == Copy
		touch := last.Orig[1] == origStart && last.Output[1] == outStart
		if sameCopy && touch {
			last.Orig[1], last.Output[1] = origEnd, outEnd
			return
		}
	}
	m.entries = append(m.entries, Entry{kind, [2]int{origStart, origEnd}, [2]int{outStart, outEnd}})
}

// ToOrig maps an output offset to an original offset.
func (m *Map) ToOrig(o int) int {
	n := len(m.entries)
	k := sort.Search(n, func(i int) bool {
		return m.entries[i].Output[1] > o || (m.entries[i].Output[1] == o && m.entries[i].Kind == Insert)
	})
	m.checked = 1
	if k == n {
		return o + m.delta(n-1)
	}
	e := &m.entries[k]
	if e.Kind == Delete {
		return e.Orig[1]
	}
	if o < e.Output[0] {
		return o + m.delta(k-1)
	}
	return e.Orig[0] + (o - e.Output[0])
}

// ToOut maps an original offset to an output offset.
func (m *Map) ToOut(i int) int {
	n := len(m.entries)
	k := sort.Search(n, func(k int) bool {
		return m.entries[k].Orig[1] > i || (m.entries[k].Orig[1] == i && m.entries[k].Kind == Insert)
	})
	m.checked = 1
	if k == n {
		return i + m.delta(n-1)
	}
	e := &m.entries[k]
	if e.Kind == Delete {
		return e.Output[1]
	}
	if i < e.Orig[0] {
		return i + m.delta(k-1)
	}
	return e.Output[0] + (i - e.Orig[0])
}

func (m *Map) delta(k int) int {
	if k < 0 {
		return 0
	}
	e := &m.entries[k]
	return (e.Output[1] - e.Output[0]) - (e.Orig[1] - e.Orig[0])
}

// RebuildTail replaces mappings at or after origStart with a retained suffix.
func (m *Map) RebuildTail(origStart, outEnd int, out []byte) {
	k := 0
	for k < len(m.entries) && m.entries[k].Orig[1] <= origStart {
		k++
	}
	outStart := 0
	if k > 0 {
		outStart = m.entries[k-1].Output[1]
	}
	m.entries = append(m.entries[:k:k], Entry{Copy, [2]int{origStart, origStart + len(out) - outStart}, [2]int{outStart, outEnd}})
}

// Slice maps original interval [start,end) into local coordinates.
func (m *Map) Slice(start, end int) *Map {
	q := NewMap()
	oo := m.ToOut(start)
	for i := start; i < end; {
		o := m.ToOut(i)
		j, step := i+1, 1
		if m.ToOut(i+1) == o {
			for j < end && m.ToOut(j) == o {
				j++
			}
			step = 0
		}
		q.Add(mapKind(step), i-start, j-start, o-oo, m.ToOut(j)-oo)
		i = j
	}
	return q
}

func mapKind(step int) int {
	if step == 0 {
		return Delete
	}
	return Copy
}

// Concat joins maps whose coordinate spaces are contiguous in both dimensions.
func Concat(parts []Part) *Map {
	out := NewMap()
	var os, oo int
	for _, part := range parts {
		for _, e := range part.Map.entries {
			out.Add(e.Kind, os+e.Orig[0], os+e.Orig[1], oo+e.Output[0], oo+e.Output[1])
		}
		os += part.Orig
		oo += part.Output
	}
	return out
}
