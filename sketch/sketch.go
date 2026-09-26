// Package sketch implements the Count-Min Sketch table: Add accumulates
// every row, Query returns the per-row minimum. Depends only on ch.
package sketch

import (
	"sync"

	"ontology/ch"
)

// Sketch is a Count-Min Sketch of width w and depth d.
type Sketch struct {
	w, d  int
	table [][]int64
	mu    sync.RWMutex
	// lastQueryCells records how many cells the most recent Query
	// touched. Non-exported on purpose: it is a complexity witness,
	// not part of the public interface.
	lastQueryCells int
}

// New returns a zeroed sketch. w and d must be positive; callers are
// expected to validate (api.New does).
func New(w, d int) *Sketch {
	t := make([][]int64, d)
	for j := range t {
		t[j] = make([]int64, w)
	}
	return &Sketch{w: w, d: d, table: t}
}

// Add accumulates count into the cell row j hashes key to, for every row.
func (s *Sketch) Add(key, count int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for j := 1; j <= s.d; j++ {
		s.table[j-1][ch.Col(j, s.w, key)] += count
	}
}

// Query returns the minimum over all rows of the cell key hashes to.
// It touches exactly d cells and records that in lastQueryCells.
func (s *Sketch) Query(key int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	cells := 0
	min := s.table[0][ch.Col(1, s.w, key)]
	cells++
	for j := 2; j <= s.d; j++ {
		v := s.table[j-1][ch.Col(j, s.w, key)]
		cells++
		if v < min {
			min = v
		}
	}
	s.lastQueryCells = cells
	return min
}

// QueryCostIsDepth reports whether the most recent Query touched exactly
// d cells (O(d), independent of how many keys were added). It exposes only
// a verdict, never the counter's value.
func (s *Sketch) QueryCostIsDepth() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastQueryCells == s.d
}
