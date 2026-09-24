// Package mset tracks a single-side multiset of rows: per-row multiplicity,
// deletion-underflow protection and distinct-row bookkeeping. It depends on
// no other package in this module.
package mset

import "errors"

// ErrUnderflow is returned when a deletion would make a row's multiplicity
// negative. The rejected operation changes no state.
var ErrUnderflow = errors.New("mset: deletion underflow: multiplicity would become negative")

// Multiset maps non-empty row strings to positive multiplicities. A row with
// multiplicity zero is never kept in the map.
type Multiset struct {
	m map[string]int
}

// New returns an empty multiset.
func New() *Multiset {
	return &Multiset{m: make(map[string]int)}
}

// Get reports the current multiplicity of row (0 when the row is absent).
func (m *Multiset) Get(row string) int { return m.m[row] }

// Len reports how many distinct rows currently have non-zero multiplicity.
func (m *Multiset) Len() int { return len(m.m) }

// Add applies a single multiplicity change: delta>0 inserts copies, delta<0
// deletes them. A deletion that would make the result negative is rejected
// with ErrUnderflow and changes nothing. delta==0 is a no-op.
func (m *Multiset) Add(row string, delta int) error {
	if delta == 0 {
		return nil
	}
	next := m.m[row] + delta
	if next < 0 {
		return ErrUnderflow
	}
	if next == 0 {
		delete(m.m, row)
	} else {
		m.m[row] = next
	}
	return nil
}

// Snapshot returns an independent copy of the multiplicity map.
func (m *Multiset) Snapshot() map[string]int {
	out := make(map[string]int, len(m.m))
	for row, n := range m.m {
		out[row] = n
	}
	return out
}
