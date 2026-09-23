// Package sketch implements a Count-Min Sketch: a d by w counter matrix.
// Add touches one cell per row; Estimate takes the per-row minimum, so an
// estimate is never below the true count.
package sketch

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/hashfam"
)

var (
	// ErrBadDimensions is returned for non-positive width or depth.
	ErrBadDimensions = errors.New("sketch: width and depth must be positive")
	// ErrEmptyKey is returned for the empty key.
	ErrEmptyKey = errors.New("sketch: key must not be empty")
	// ErrMismatch is returned when merging sketches with different parameters.
	ErrMismatch = errors.New("sketch: merge requires identical width and depth")
)

// Sketch is a d by w counter matrix safe for concurrent use.
type Sketch struct {
	w, d  int
	rows  [][]int64
	fns   []hashfam.Func
	total int64
	mu    sync.RWMutex

	lastVisited atomic.Int64 // cells probed by the latest Add/Estimate; unexported
}

// New builds a sketch with the given width and depth.
func New(width, depth int) (*Sketch, error) {
	if width <= 0 || depth <= 0 {
		return nil, ErrBadDimensions
	}
	fns, err := hashfam.New(depth)
	if err != nil {
		return nil, err
	}
	rows := make([][]int64, depth)
	for i := range rows {
		rows[i] = make([]int64, width)
	}
	return &Sketch{w: width, d: depth, rows: rows, fns: fns}, nil
}

// Width and Depth report the matrix parameters.
func (s *Sketch) Width() int { return s.w }
func (s *Sketch) Depth() int { return s.d }

// Total reports the number of accepted inserts.
func (s *Sketch) Total() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total
}

// CellCount returns how many cells the latest Add/Estimate probed.
// Unexported: tests inside the module use it; it is not part of the API.
func (s *Sketch) CellCount() int64 { return s.lastVisited.Load() }

// Add increments the d cells of key by n.
func (s *Sketch) Add(key string, n int64) error {
	if key == "" {
		return ErrEmptyKey
}
	if n < 0 {
		return errors.New("sketch: increment must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, fn := range s.fns {
		s.rows[i][int(fn(key)%uint64(s.w))] += n
	}
	s.total += n
	s.lastVisited.Store(int64(s.d))
	return nil
}

// AddConservative raises only cells below m+n, where m is the per-row min.
// It preserves no-underestimate but is not merge-compatible (see DESIGN.md).
func (s *Sketch) AddConservative(key string, n int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if n < 0 {
		return errors.New("sketch: increment must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := make([]int, s.d)
	m := int64(1<<62)
	for i, fn := range s.fns {
		idx[i] = int(fn(key) % uint64(s.w))
		if v := s.rows[i][idx[i]]; v < m {
			m = v
		}
	}
	target := m + n
	for i, c := range idx {
		if s.rows[i][c] < target {
			s.rows[i][c] = target
		}
	}
	s.total += n
	s.lastVisited.Store(int64(s.d))
	return nil
}

// Estimate returns the minimum over the d cells of key (never underestimates).
func (s *Sketch) Estimate(key string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var m int64 = 1<<62
	for i, fn := range s.fns {
		if v := s.rows[i][int(fn(key)%uint64(s.w))]; v < m {
			m = v
		}
	}
	s.lastVisited.Store(int64(s.d))
	return m, nil
}

// Merge adds every cell of other into s; parameters must match exactly and
// neither sketch is modified on mismatch.
func (s *Sketch) Merge(other *Sketch) error {
	if other == nil {
		return errors.New("sketch: cannot merge nil sketch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()
	if s.w != other.w || s.d != other.d {
		return ErrMismatch
	}
	for i := range s.rows {
		for j, v := range other.rows[i] {
			s.rows[i][j] += v
		}
	}
	s.total += other.total
	return nil
}

// Snapshot returns a defensive copy of the matrix, row by row.
func (s *Sketch) Snapshot() [][]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([][]int64, s.d)
	for i := range out {
		out[i] = append([]int64(nil), s.rows[i]...)
	}
	return out
}

// RowSum returns the sum of every cell in row i, used by self checks.
func (s *Sketch) RowSum(i int) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum int64
	for _, v := range s.rows[i] {
		sum += v
	}
	return sum
}
