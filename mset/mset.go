// Package mset tracks one side of a multiset: multiplicity per row,
// delete-underflow checks and a cap on the number of distinct rows.
package mset

import "errors"

var (
	// ErrUnderflow: a delete would drive a row's multiplicity negative.
	ErrUnderflow = errors.New("mset: delete underflow")
	// ErrTooManyRows: the change would exceed the distinct-row cap.
	ErrTooManyRows = errors.New("mset: too many distinct rows")
)

// MSet is a multiset of non-empty string rows.
type MSet struct {
	cnt     map[string]int
	maxRows int
}

// New returns an empty multiset holding at most maxRows distinct rows.
func New(maxRows int) *MSet { return &MSet{cnt: map[string]int{}, maxRows: maxRows} }

// Get returns the current multiplicity of row (0 when absent).
func (m *MSet) Get(row string) int { return m.cnt[row] }

// Rows returns the number of distinct rows with non-zero multiplicity.
func (m *MSet) Rows() int { return len(m.cnt) }

// Add applies delta (non-zero; positive inserts, negative deletes) to row.
// It fails without touching state on underflow or when the change would
// push the distinct-row count above the cap.
func (m *MSet) Add(row string, delta int) error {
	cur := m.cnt[row]
	if cur+delta < 0 {
		return ErrUnderflow
	}
	if cur == 0 && delta > 0 && len(m.cnt) >= m.maxRows {
		return ErrTooManyRows
	}
	if cur+delta == 0 {
		delete(m.cnt, row)
	} else {
		m.cnt[row] = cur + delta
	}
	return nil
}
