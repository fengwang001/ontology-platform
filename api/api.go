// Package api is the public face of the Count-Min Sketch: validated
// construction, Add/Query with sentinel errors, and SelfCheck.
package api

import (
	"errors"
	"fmt"

	"ontology/sketch"
)

// Sentinel errors, each identifying exactly one rejection cause.
var (
	ErrBadDim   = errors.New("api: width and depth must be positive")
	ErrBadCount = errors.New("api: count must be positive")
	ErrBadKey   = errors.New("api: key must be non-negative")
)

// API wraps a sketch with input validation.
type API struct {
	s *sketch.Sketch
}

// New creates a sketch of width w and depth d. Fails wholesale on
// non-positive dimensions.
func New(w, d int) (*API, error) {
	if w <= 0 || d <= 0 {
		return nil, ErrBadDim
	}
	return &API{s: sketch.New(w, d)}, nil
}

// Add accumulates count for key. Rejected calls change no state.
func (a *API) Add(k, c int64) error {
	if k < 0 {
		return ErrBadKey
	}
	if c <= 0 {
		return ErrBadCount
	}
	a.s.Add(k, c)
	return nil
}

// Query returns the estimated frequency of key, never below the true one.
func (a *API) Query(k int64) (int64, error) {
	if k < 0 {
		return 0, ErrBadKey
	}
	return a.s.Query(k), nil
}

// QueryCostIsDepth reports whether the last Query touched exactly d cells.
func (a *API) QueryCostIsDepth() bool { return a.s.QueryCostIsDepth() }

// SelfCheck verifies the four invariants on built-in sequences using its
// own throwaway sketches, so it is safe to call concurrently.
func SelfCheck() error {
	// Invariant 2: single key is exact.
	a, _ := New(6, 3)
	if err := a.Add(7, 9); err != nil {
		return err
	}
	if q, _ := a.Query(7); q != 9 {
		return fmt.Errorf("selfcheck: single key got %d want 9", q)
	}
	// Invariants 1+3: query never below the exact reference map.
	b, _ := New(4, 3)
	ref := map[int64]int64{}
	for _, e := range [][2]int64{{2, 4}, {5, 2}, {11, 1}, {2, 3}, {6, 5}} {
		if err := b.Add(e[0], e[1]); err != nil {
			return err
		}
		ref[e[0]] += e[1]
	}
	for k, c := range ref {
		if q, _ := b.Query(k); q < c {
			return fmt.Errorf("selfcheck: key %d got %d below true %d", k, q, c)
		}
	}
	// Invariant 3, collision-free: query equals the reference exactly.
	c, _ := New(9, 3)
	for k := int64(0); k < 9; k++ {
		if err := c.Add(k, k+1); err != nil {
			return err
		}
	}
	for k := int64(0); k < 9; k++ {
		if q, _ := c.Query(k); q != k+1 {
			return fmt.Errorf("selfcheck: collision-free key %d got %d want %d", k, q, k+1)
		}
	}
	// Invariant 4: rejected ops leave state untouched.
	d, _ := New(6, 3)
	if err := d.Add(2, 4); err != nil {
		return err
	}
	before, _ := d.Query(2)
	if d.Add(-1, 1) != ErrBadKey || d.Add(2, 0) != ErrBadCount {
		return errors.New("selfcheck: rejection not signaled")
	}
	if after, _ := d.Query(2); after != before {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}
