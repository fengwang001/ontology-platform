// Package sketch implements a Count-Min sketch: a d x w counter matrix.
// Add increments d cells per key; Estimate returns their minimum, so the
// estimate is never below the true count. Plain updates only: no Sub, no
// conservative update (see DESIGN.md).
package sketch

import (
	"errors"
	"math"
	"sync/atomic"

	"ontology/hashfam"
)

var (
	ErrInvalidDims  = errors.New("sketch: width and depth must be positive")
	ErrEmptyKey     = errors.New("sketch: empty key")
	ErrIncompatible = errors.New("sketch: parameter mismatch on merge")
	ErrOverflow     = errors.New("sketch: max total count exceeded")
)

// Sketch is a Count-Min counting matrix. Not safe for concurrent writes;
// read-only operations may run concurrently after construction.
type Sketch struct {
	w, d     int
	cells    [][]uint64
	total    uint64
	maxTotal uint64
	fam      hashfam.Family
	accessed atomic.Uint64 // cells touched by the last Add/Estimate
}

// New builds an empty d x w sketch. maxTotal caps the total count; 0 means
// no cap.
func New(w, d int, maxTotal uint64) (*Sketch, error) {
	if w <= 0 || d <= 0 {
		return nil, ErrInvalidDims
	}
	if maxTotal == 0 {
		maxTotal = math.MaxUint64
	}
	cells := make([][]uint64, d)
	for i := range cells {
		cells[i] = make([]uint64, w)
	}
	return &Sketch{w: w, d: d, cells: cells, maxTotal: maxTotal, fam: hashfam.New(d)}, nil
}

// Dims returns width and depth.
func (s *Sketch) Dims() (w, d int) { return s.w, s.d }

// Total returns the sum of all added counts.
func (s *Sketch) Total() uint64 { return s.total }

// Add increments the d cells of key by n. Rejected calls change nothing.
func (s *Sketch) Add(key string, n uint64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if n > s.maxTotal-s.total {
		return ErrOverflow
	}
	s.accessed.Store(0)
	for r := 0; r < s.d; r++ {
		s.cells[r][s.fam.Hash(r, key, s.w)] += n
		s.accessed.Add(1)
	}
	s.total += n
	return nil
}

// Estimate returns the minimum of the d cells of key: never below the true
// count, possibly above it by collision noise.
func (s *Sketch) Estimate(key string) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.accessed.Store(0)
	est := uint64(math.MaxUint64)
	for r := 0; r < s.d; r++ {
		if v := s.cells[r][s.fam.Hash(r, key, s.w)]; v < est {
			est = v
		}
		s.accessed.Add(1)
	}
	return est, nil
}

// Merge adds other's cells into s. Parameters must match; on any rejection
// neither sketch is modified.
func (s *Sketch) Merge(o *Sketch) error {
	if o.w != s.w || o.d != s.d {
		return ErrIncompatible
	}
	if o.total > s.maxTotal-s.total {
		return ErrOverflow
	}
	for r := 0; r < s.d; r++ {
		for c := 0; c < s.w; c++ {
			s.cells[r][c] += o.cells[r][c]
		}
	}
	s.total += o.total
	return nil
}

// RowSum returns the sum of one row; used by SelfCheck. Always equals Total.
func (s *Sketch) RowSum(row int) uint64 {
	var sum uint64
	for _, v := range s.cells[row] {
		sum += v
	}
	return sum
}

// Equal reports whether both sketches have identical parameters and cells.
func (s *Sketch) Equal(o *Sketch) bool {
	if s.w != o.w || s.d != o.d || s.total != o.total {
		return false
	}
	for r := 0; r < s.d; r++ {
		for c := 0; c < s.w; c++ {
			if s.cells[r][c] != o.cells[r][c] {
				return false
			}
		}
	}
	return true
}

// Clone returns a deep copy.
func (s *Sketch) Clone() *Sketch {
	c := &Sketch{w: s.w, d: s.d, total: s.total, maxTotal: s.maxTotal, fam: s.fam}
	c.cells = make([][]uint64, s.d)
	for r := range s.cells {
		c.cells[r] = append([]uint64(nil), s.cells[r]...)
	}
	return c
}
