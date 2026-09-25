// Package sketch is a count-min sketch: a d x w counting matrix with
// Add/Estimate/Merge. Estimates never underestimate true counts.
package sketch

import (
	"errors"
	"sync/atomic"

	"ontology/hashfam"
)

var (
	ErrBadDimensions = errors.New("sketch: width and depth must be positive")
	ErrEmptyKey      = errors.New("sketch: empty key")
	ErrIncompatible  = errors.New("sketch: parameter mismatch")
	ErrCorrupt       = errors.New("sketch: internal invariant violated")
)

// Sketch is a d x w matrix of counters, row-major in cells.
type Sketch struct {
	w, d     int
	cells    []uint64
	total    uint64
	fam      *hashfam.Family
	accessed int64 // cells touched by the most recent Add/Estimate
}

// New builds an empty sketch. Zero or negative dimensions are rejected.
func New(w, d int) (*Sketch, error) {
	if w <= 0 || d <= 0 {
		return nil, ErrBadDimensions
	}
	return &Sketch{w: w, d: d, cells: make([]uint64, w*d), fam: hashfam.New(w, d)}, nil
}

// Dims returns width and depth.
func (s *Sketch) Dims() (int, int) { return s.w, s.d }

// Total returns the sum of all added counts.
func (s *Sketch) Total() uint64 { return s.total }

// Add increments the d cells of key by n.
func (s *Sketch) Add(key string, n uint64) error {
	if key == "" {
		return ErrEmptyKey
	}
	for r := 0; r < s.d; r++ {
		s.cells[r*s.w+int(s.fam.Index(r, key))] += n
	}
	s.total += n
	atomic.StoreInt64(&s.accessed, int64(s.d))
	return nil
}

// Estimate returns an upper bound on the true count of key; never below it.
func (s *Sketch) Estimate(key string) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	m := s.cells[int(s.fam.Index(0, key))]
	for r := 1; r < s.d; r++ {
		if v := s.cells[r*s.w+int(s.fam.Index(r, key))]; v < m {
			m = v
		}
	}
	atomic.StoreInt64(&s.accessed, int64(s.d))
	return m, nil
}

// Merge adds other's cells into s. Mismatched dimensions are rejected and
// leave both sketches untouched.
func (s *Sketch) Merge(other *Sketch) error {
	if s.w != other.w || s.d != other.d {
		return ErrIncompatible
	}
	for i := range s.cells {
		s.cells[i] += other.cells[i]
	}
	s.total += other.total
	return nil
}

// Row returns a copy of row r, for inspection by callers and tests.
func (s *Sketch) Row(r int) []uint64 {
	row := make([]uint64, s.w)
	copy(row, s.cells[r*s.w:(r+1)*s.w])
	return row
}

// SelfCheck verifies: positive dims, non-negative cells, and every row
// summing to the total number of insertions.
func (s *Sketch) SelfCheck() error {
	if s.w <= 0 || s.d <= 0 || len(s.cells) != s.w*s.d {
		return ErrCorrupt
	}
	for r := 0; r < s.d; r++ {
		var sum uint64
		for _, v := range s.Row(r) {
			sum += v
		}
		if sum != s.total {
			return ErrCorrupt
		}
	}
	return nil
}
