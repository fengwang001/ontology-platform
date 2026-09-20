// Package dedup implements a concurrent, streaming multi-column
// deduplicator with deterministic output ordering.
package dedup

import "sync"

// Mode selects which row of a duplicate group is kept.
type Mode int

const (
	// KeepFirst keeps the first row seen for each group.
	KeepFirst Mode = iota
	// KeepLast keeps the most recent row seen for each group.
	KeepLast
)

// group holds the single kept row of one dedup group plus the data
// needed to order it deterministically.
type group struct {
	vals []sortVal
	tie  string
	row  map[string]any
}

// Deduper is a streaming multi-column deduplicator. The zero value is
// not usable; construct with New. All methods are safe for concurrent
// use.
type Deduper struct {
	mu        sync.Mutex
	columns   []string
	mode      Mode
	groups    map[string]*group
	processed int64
	seq       uint64

	missingRows int64
	nilRows     int64
	emptyRows   int64
	nanGroups   int64
}

// New returns a Deduper grouping on the given ordered columns.
func New(columns []string, mode Mode) *Deduper {
	cols := make([]string, len(columns))
	copy(cols, columns)
	return &Deduper{columns: cols, mode: mode, groups: make(map[string]*group)}
}

// Add feeds one row. Memory stays bounded by the number of groups, not
// by the number of rows seen.
func (d *Deduper) Add(row map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.processed++
	class := classifyRow(d.columns, row)
	switch class {
	case classMissing:
		d.missingRows++
	case classNil:
		d.nilRows++
	case classNaN:
		d.nanGroups++
	case classNormal:
		if hasEmptyString(d.columns, row) {
			d.emptyRows++
		}
	}
	var key string
	if class == classNaN || class == classOther {
		d.seq++
		key = buildKey(d.columns, row, class, d.seq)
	} else {
		key = buildKey(d.columns, row, class, 0)
	}
	g, ok := d.groups[key]
	if !ok {
		d.groups[key] = &group{vals: sortVals(d.columns, row), tie: rowTie(row), row: copyRow(row)}
		return
	}
	if d.mode == KeepLast {
		g.vals = sortVals(d.columns, row)
		g.tie = rowTie(row)
		g.row = copyRow(row)
	}
}

// Snapshot returns the current deduplicated rows, sorted by the dedup
// columns in ascending order, column by column. The result is fully
// detached from internal state.
func (d *Deduper) Snapshot() []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	gs := make([]*group, 0, len(d.groups))
	for _, g := range d.groups {
		gs = append(gs, g)
	}
	sortGroups(gs)
	out := make([]map[string]any, len(gs))
	for i, g := range gs {
		out[i] = copyRow(g.row)
	}
	return out
}

// Groups returns the current number of distinct groups.
func (d *Deduper) Groups() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.groups)
}

// Processed returns the total number of rows passed to Add.
func (d *Deduper) Processed() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.processed
}

// NaNGroups returns the number of groups holding a NaN dedup column.
// Every NaN row forms its own group, so this equals the NaN row count.
func (d *Deduper) NaNGroups() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nanGroups
}

// MissingRows returns how many added rows fell into the missing-column
// group (at least one dedup column absent).
func (d *Deduper) MissingRows() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.missingRows
}

// NilRows returns how many added rows fell into the nil-value group.
func (d *Deduper) NilRows() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.nilRows
}

// EmptyStringRows returns how many added rows fell into an
// empty-string group (no missing/nil column, at least one empty
// string among the dedup columns).
func (d *Deduper) EmptyStringRows() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.emptyRows
}
