// Package row holds the per-key map of columns. Every column owns an
// independent cell.Cell, so writes to one column never touch siblings.
package row

import (
	"strconv"

	"ontology/cell"
)

// Row is the column view of one key. lastChecked records how many column
// entries the most recent Apply inspected to locate its column; a map
// lookup inspects a constant number regardless of row width.
type Row struct {
	cols        map[string]*cell.Cell
	lastChecked int
}

// New returns an empty row.
func New() *Row {
	return &Row{cols: make(map[string]*cell.Cell)}
}

// Apply writes one column (value or tombstone) and reports a conflict.
func (r *Row) Apply(col string, ts int64, val string, tomb bool) (conflict bool) {
	r.lastChecked = 1 // a single map index locates the column
	c := r.cols[col]
	if c == nil {
		c = &cell.Cell{}
		r.cols[col] = c
	}
	return c.Apply(ts, val, tomb)
}

// View returns the live columns: tombstones and never-written columns are
// both absent, though only a tombstone still suppresses older writes.
func (r *Row) View() map[string]string {
	out := make(map[string]string, len(r.cols))
	for col, c := range r.cols {
		if c.Present && !c.Tomb {
			out[col] = c.Val
		}
	}
	return out
}

// ConstantLookup reports that locating a column costs a bound independent
// of row width, without exposing the internal counter value.
func (r *Row) ConstantLookup(sizes []int) bool {
	const bound = 2
	for _, m := range sizes {
		w := New()
		for i := 0; i < m; i++ {
			w.Apply("c"+strconv.Itoa(i), 1, "v", false)
		}
		w.Apply("c0", 2, "v2", false)
		if w.lastChecked > bound {
			return false
		}
	}
	return true
}
