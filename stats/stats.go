// Package stats holds table statistics (row counts, column NDVs and
// equi-width histograms) and derives selectivity estimates from them.
// It never touches row data; RowAccesses stays zero as proof.
package stats

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// DefaultSelectivity is the hard-coded fallback used when a column has no
// statistics. Estimates made with it are flagged unreliable.
const DefaultSelectivity = 0.1

// ErrCorrupt marks statistics that fail validation (histogram mismatch or
// non-increasing bucket bounds). Wrapped errors carry table/column names.
var ErrCorrupt = errors.New("corrupt statistics")

var rowAccesses atomic.Int64

// RowAccesses reports how many times estimation touched row-level data.
// Estimation in this module only reads aggregates, so it is always 0.
func RowAccesses() int64 { return rowAccesses.Load() }

// Histogram is an equi-width histogram: Bounds has len(Buckets)+1 strictly
// increasing entries; Buckets[i] counts rows in [Bounds[i], Bounds[i+1]).
type Histogram struct {
	Bounds  []float64
	Buckets []uint64
}

// NewEquiWidth builds an equi-width histogram over [min, max].
func NewEquiWidth(min, max float64, buckets []uint64) *Histogram {
	h := &Histogram{Buckets: buckets}
	n := len(buckets)
	h.Bounds = make([]float64, n+1)
	for i := 0; i <= n; i++ {
		h.Bounds[i] = min + (max-min)*float64(i)/float64(n)
	}
	return h
}

// Validate checks bucket-count sum against totalRows and strict increase of
// bounds. The error wraps ErrCorrupt and names the table and column.
func (h *Histogram) Validate(table, column string, totalRows uint64) error {
	if len(h.Bounds) != len(h.Buckets)+1 {
		return fmt.Errorf("%w: table %s column %s: %d bounds for %d buckets",
			ErrCorrupt, table, column, len(h.Bounds), len(h.Buckets))
	}
	for i := 1; i < len(h.Bounds); i++ {
		if h.Bounds[i] <= h.Bounds[i-1] {
			return fmt.Errorf("%w: table %s column %s: bucket bounds not increasing at %d",
				ErrCorrupt, table, column, i)
		}
	}
	var sum uint64
	for _, b := range h.Buckets {
		sum += b
	}
	if sum != totalRows {
		return fmt.Errorf("%w: table %s column %s: bucket sum %d != rows %d",
			ErrCorrupt, table, column, sum, totalRows)
	}
	return nil
}

// Column holds per-column statistics.
type Column struct {
	NDV       float64
	Histogram *Histogram
}

// Table holds per-table statistics. Rows is the row count claimed by the
// statistics themselves; the catalog may hold a fresher value.
type Table struct {
	Name    string
	Rows    int64
	Columns map[string]Column
}

// Validate checks every column histogram of the table.
func (t *Table) Validate() error {
	if t.Rows < 0 {
		return fmt.Errorf("%w: table %s: negative rows %d", ErrCorrupt, t.Name, t.Rows)
	}
	for name, col := range t.Columns {
		if col.NDV < 0 {
			return fmt.Errorf("%w: table %s column %s: negative NDV", ErrCorrupt, t.Name, name)
		}
		if col.Histogram != nil {
			if err := col.Histogram.Validate(t.Name, name, uint64(t.Rows)); err != nil {
				return err
			}
		}
	}
	return nil
}
