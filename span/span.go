// Package span records the correspondence between original byte ranges
// and output byte ranges of the normalizer and answers bidirectional
// offset queries by binary search.
//
// An Entry describes one output run [OutStart,OutEnd):
//   - OrigLen > 0: retained bytes copied 1:1 from [OrigStart, OrigStart+OrigLen);
//   - OrigLen == 0: a synthesized run (e.g. an inserted final newline),
//     pinned to original offset OrigStart.
// Gaps between entries are deleted original bytes; queries map them to
// the boundary on their right (see DESIGN.md §2).
package span

// Entry is one output run and its original source.
type Entry struct {
	OutStart  int
	OutEnd    int
	OrigStart int
	OrigLen   int
}

// Table is an immutable set of entries built by Builder.
type Table struct {
	entries []Entry
	// origEnd is one past the last covered original offset (len(input)).
	origEnd int
	// lastCheck counts entries inspected by the most recent query.
	lastCheck int
}

// Entries returns the entries in increasing original order.
func (t *Table) Entries() []Entry { return t.entries }

// OrigLen returns len(input).
func (t *Table) OrigLen() int { return t.origEnd }

// OutLen returns len(output).
func (t *Table) OutLen() int {
	if len(t.entries) == 0 {
		return 0
	}
	return t.entries[len(t.entries)-1].OutEnd
}

// LastCheck reports how many entries the most recent ToOrig/ToOut call
// inspected; it exists to prove the logarithmic bound in tests.
func (t *Table) LastCheck() int { return t.lastCheck }

// Builder constructs a Table by emitting retained or synthesized runs
// in increasing original order.
type Builder struct {
	e       []Entry
	origEnd int
	outEnd  int
}

// OrigEnd declares the total input length (the right edge used for
// queries at EOF); call once when all input is known.
func (b *Builder) OrigEnd(n int) { b.origEnd = n }

// Retain records length bytes kept 1:1 starting at original offset orig.
// Adjacent retained runs are merged, so the entry count tracks deletion
// points, not output bytes.
func (b *Builder) Retain(orig, length int) {
	if length <= 0 {
		return
	}
	if n := len(b.e); n > 0 {
		last := &b.e[n-1]
		if last.OrigLen > 0 && last.OrigStart+last.OrigLen == orig && last.OutEnd == b.outEnd {
			last.OrigLen += length
			last.OutEnd += length
			b.outEnd += length
			b.origEnd = max(b.origEnd, orig+length)
			return
		}
	}
	b.e = append(b.e, Entry{OutStart: b.outEnd, OutEnd: b.outEnd + length, OrigStart: orig, OrigLen: length})
	b.outEnd += length
	b.origEnd = max(b.origEnd, orig+length)
}

// Insert records a synthesized run (zero original length) pinned at
// original offset orig, producing outLen output bytes.
func (b *Builder) Insert(orig, outLen int) {
	if outLen <= 0 {
		return
	}
	b.e = append(b.e, Entry{OutStart: b.outEnd, OutEnd: b.outEnd + outLen, OrigStart: orig})
	b.outEnd += outLen
	b.origEnd = max(b.origEnd, orig)
}

// ResetTail drops every run at or after original offset orig and restores
// the output cursor to outLen (used when the final policy rewrites the tail).
func (b *Builder) ResetTail(orig, outLen int) {
	i := 0
	for i < len(b.e) && b.e[i].OrigStart < orig {
		i++
	}
	b.e = b.e[:i]
	b.origEnd = orig
	b.outEnd = outLen
}

// Build freezes the builder into a Table.
func (b *Builder) Build() *Table {
	return &Table{entries: b.e, origEnd: b.origEnd}
}

func (t *Table) entryAtOrig(o int) (Entry, bool) {
	lo, hi := 0, len(t.entries)
	t.lastCheck = 0
	for lo < hi {
		mid := (lo + hi) / 2
		t.lastCheck++
		e := t.entries[mid]
		if e.OrigStart+max(e.OrigLen, 1) <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(t.entries) && t.entries[lo].OrigStart <= o {
		return t.entries[lo], true
	}
	return Entry{}, false
}

// ToOrig maps output offset o (0..OutLen) to an original offset.
func (t *Table) ToOrig(o int) int {
	lo, hi := 0, len(t.entries)
	t.lastCheck = 0
	for lo < hi {
		mid := (lo + hi) / 2
		t.lastCheck++
		if t.entries[mid].OutEnd <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(t.entries) {
		e := t.entries[lo]
		if o < e.OutStart {
			return e.OrigStart
		}
		if e.OrigLen > 0 {
			return e.OrigStart + (o - e.OutStart)
		}
		return e.OrigStart
	}
	return t.origEnd
}

// ToOut maps original offset i (0..OrigLen) to an output offset. Deleted
// runs map to the boundary on their right (DESIGN.md §2).
func (t *Table) ToOut(i int) int {
	e, ok := t.entryAtOrig(i)
	t.lastCheck++
	if !ok {
		return t.OutLen()
	}
	if i < e.OrigStart {
		return e.OutStart
	}
	if e.OrigLen > 0 && i >= e.OrigStart {
		if i < e.OrigStart+e.OrigLen {
			return e.OutStart + (i - e.OrigStart)
		}
	}
	if e.OrigLen == 0 {
		return e.OutEnd
	}
	return e.OutEnd
}

// Slice keeps runs whose original range intersects [lo,hi), shifts their
// coordinates to be relative to the slice, and returns the sub-table plus
// the original/output base offsets that were removed.
func (t *Table) Slice(lo, hi int) (sub *Table, origBase, outBase int) {
	var e2 []Entry
	for _, e := range t.entries {
		if e.OrigLen == 0 {
			if e.OrigStart < lo || e.OrigStart >= hi {
				continue
			}
			if len(e2) == 0 {
				outBase = e.OutStart
			}
			e2 = append(e2, Entry{OutStart: e.OutStart - outBase, OutEnd: e.OutEnd - outBase, OrigStart: e.OrigStart - lo})
			continue
		}
		start := max(e.OrigStart, lo)
		end := min(e.OrigStart+e.OrigLen, hi)
		if start >= end {
			continue
		}
		if len(e2) == 0 {
			outBase = e.OutStart
		}
		e2 = append(e2, Entry{
			OutStart:  e.OutStart + (start - e.OrigStart) - outBase,
			OutEnd:    e.OutStart + (end - e.OrigStart) - outBase,
			OrigStart: start - lo,
			OrigLen:   end - start,
		})
	}
	return &Table{entries: e2, origEnd: hi - lo}, lo, outBase
}

// Concat joins sub-tables whose original coordinates are already
// consecutive and non-overlapping, accumulating their output positions.
func Concat(subs ...*Table) *Table {
	var all []Entry
	origShift, outShift, origEnd := 0, 0, 0
	for _, s := range subs {
		for _, e := range s.entries {
			all = append(all, Entry{
				OutStart:  e.OutStart + outShift,
				OutEnd:    e.OutEnd + outShift,
				OrigStart: e.OrigStart + origShift,
				OrigLen:   e.OrigLen,
			})
		}
		origShift += s.origEnd
		outShift += s.OutLen()
		origEnd = origShift
	}
	return &Table{entries: all, origEnd: origEnd}
}
