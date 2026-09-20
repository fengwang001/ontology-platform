// Package dedup implements an in-memory multi-column deduplicator.
//
// Rows are maps of string to any. A Deduper keeps exactly one row per
// distinct dedup key, either the first or the last row seen, and can
// produce a snapshot sorted by the dedup columns at any time.
package dedup

import "sync"

// Mode selects which row of a duplicate group is kept.
type Mode int

const (
	// KeepFirst keeps the first row seen for each dedup key.
	KeepFirst Mode = iota
	// KeepLast keeps the most recent row seen for each dedup key.
	KeepLast
)

// EmptyClass classifies how a row's dedup key relates to "empty" values.
type EmptyClass int

const (
	// EmptyNone means no dedup column is empty.
	EmptyNone EmptyClass = iota
	// EmptyMissing means at least one dedup column is absent.
	EmptyMissing
	// EmptyNil means no column is absent but at least one is nil.
	EmptyNil
	// EmptyString means no column is absent or nil but at least one is "".
	EmptyString
)

// String returns a human-readable name for the empty class.
func (c EmptyClass) String() string {
	switch c {
	case EmptyMissing:
		return "missing"
	case EmptyNil:
		return "nil"
	case EmptyString:
		return "empty-string"
	default:
		return "none"
	}
}

// Group is one deduplicated entry in a Snapshot.
type Group struct {
	// Key is the canonical encoding of the dedup key.
	Key string
	// Row is a copy of the kept row; mutating it is safe.
	Row map[string]any
	// Class tells which empty category the key falls into.
	Class EmptyClass
}

// entry is the internal per-group state.
type entry struct {
	parts []keyPart
	class EmptyClass
	row   map[string]any
}

// Deduper deduplicates rows by an ordered list of columns.
// It is safe for concurrent use.
type Deduper struct {
	mu        sync.Mutex
	cols      []string
	mode      Mode
	groups    map[string]*entry
	processed int64
	seq       uint64
	nanGroups int
}

// New creates a Deduper over the given ordered dedup columns.
func New(cols []string, mode Mode) *Deduper {
	c := make([]string, len(cols))
	copy(c, cols)
	return &Deduper{
		cols:   c,
		mode:   mode,
		groups: make(map[string]*entry),
	}
}

// Add feeds one row. Memory stays bounded by the number of distinct
// keys, not by the number of rows added.
func (d *Deduper) Add(row map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	parts, class := d.buildParts(row)
	key := encodeKey(parts)
	if e, ok := d.groups[key]; ok {
		if d.mode == KeepLast {
			e.row = copyRow(row)
		}
	} else {
		d.groups[key] = &entry{parts: parts, class: class, row: copyRow(row)}
		if hasKind(parts, partNaN) {
			d.nanGroups++
		}
	}
	d.processed++
}

// Snapshot returns the current groups sorted by the dedup columns,
// column by column, ascending. The result is fully detached from the
// internal state.
func (d *Deduper) Snapshot() []Group {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries := make([]*entry, 0, len(d.groups))
	keys := make(map[*entry]string, len(d.groups))
	for key, e := range d.groups {
		entries = append(entries, e)
		keys[e] = key
	}
	sortEntries(entries)
	out := make([]Group, 0, len(entries))
	for _, e := range entries {
		out = append(out, Group{Key: keys[e], Row: copyRow(e.row), Class: e.class})
	}
	return out
}

// GroupCount returns the current number of distinct groups.
func (d *Deduper) GroupCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.groups)
}

// Processed returns the total number of rows added so far.
func (d *Deduper) Processed() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.processed
}

// NaNGroupCount returns the number of groups whose key contains a NaN.
// NaN never equals anything, so each NaN row forms its own group.
func (d *Deduper) NaNGroupCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nanGroups
}

// EmptyGroupCounts returns the number of current groups per empty class.
func (d *Deduper) EmptyGroupCounts() map[EmptyClass]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	counts := make(map[EmptyClass]int, 4)
	for _, e := range d.groups {
		counts[e.class]++
	}
	return counts
}

func copyRow(row map[string]any) map[string]any {
	c := make(map[string]any, len(row))
	for k, v := range row {
		c[k] = v
	}
	return c
}
