// Package span stores bidirectional offset correspondence.
package span

import "sort"

type Span struct {
	OrigStart int
	OrigEnd   int
	OutStart  int
	OutEnd    int
}

func (s Span) deleted() bool { return s.OrigStart < s.OrigEnd && s.OutStart == s.OutEnd }

type Table struct {
	spans []Span
	count int
}

// Copy returns an independent table.
func (t *Table) Copy() *Table {
	c := &Table{spans: append([]Span(nil), t.spans...)}
	return c
}

// Len returns the number of non-identity change spans.
func (t *Table) Len() int { return len(t.spans) }

// LastCheck returns spans examined by the most recent directional query.
func (t *Table) LastCheck() int { return t.count }

// Identity is implicit between deleted ranges and is not stored.
func (t *Table) Identity(origStart, origEnd, outStart, outEnd int) {}

// Delete records original [start,end) mapped at output boundary out.
func (t *Table) Delete(start, end, out int) {
	if start >= end {
		return
	}
	t.spans = append(t.spans, Span{start, end, out, out})
}

// Translate shifts all coordinates in place.
func (t *Table) Translate(origDelta, outDelta int) {
	for i := range t.spans {
		t.spans[i].OrigStart += origDelta
		t.spans[i].OrigEnd += origDelta
		t.spans[i].OutStart += outDelta
		t.spans[i].OutEnd += outDelta
	}
}

// Append concatenates a table already translated to global coordinates.
func (t *Table) Append(other *Table) { t.spans = append(t.spans, other.spans...) }

// DropFirstIdentity removes a one-byte identity prefix from the first span.
func (t *Table) DropFirstIdentity() {
	if len(t.spans) == 0 {
		return
	}
	s := &t.spans[0]
	if s.deleted() {
		return
	}
	s.OrigStart++
	s.OutStart++
	if s.OrigStart == s.OrigEnd {
		t.spans = t.spans[1:]
	}
}

// TrimEnd keeps output through outKeep and deletes original [origKeep, origEnd).
func (t *Table) TrimEnd(outKeep, origKeep, origEnd int) {
	kept := t.spans[:0]
	for _, s := range t.spans {
		if s.OrigEnd <= origKeep {
			kept = append(kept, s)
		}
		break
	}
	t.spans = append(kept, Span{origKeep, origEnd, outKeep, outKeep})
}

// ToOrig maps an output offset to an original offset.
func (t *Table) ToOrig(o int) int {
	t.count = 0
	if len(t.spans) == 0 {
		return o
	}
	idx := sort.Search(len(t.spans), func(i int) bool {
		t.count++
		return o < t.spans[i].OutStart
	})
	if idx == len(t.spans) {
		s := t.spans[len(t.spans)-1]
		return o + s.OrigEnd - s.OutStart
	}
	s := t.spans[idx]
	return o + s.OrigStart - s.OutStart
}

// ToOut maps an original offset to an output offset.
func (t *Table) ToOut(i int) int {
	t.count = 0
	if len(t.spans) == 0 {
		return i
	}
	idx := sort.Search(len(t.spans), func(j int) bool {
		t.count++
		s := t.spans[j]
		return i <= s.OrigEnd
	})
	if idx == len(t.spans) {
		s := t.spans[len(t.spans)-1]
		return i - (s.OrigEnd - s.OutStart)
	}
	s := t.spans[idx]
	if i == s.OrigEnd {
		return s.OutStart
	}
	if i < s.OrigStart && idx == 0 {
		return i
	}
	return s.OutStart
}
