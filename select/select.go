// Package sel picks elements out of a sorted population by the indices
// computed with package samp. Directory name is "select" per the spec;
// the package clause uses "sel" because select is a Go keyword.
package sel

import (
	"errors"
	"sync"

	"ontology/samp"
)

// ErrPopulationLength is returned by Sample when len(vals) differs from
// the population size N the Selector was built with.
var ErrPopulationLength = errors.New("sel: population length mismatch")

// Selector draws s elements out of a sorted population of n elements by
// direct indexing. It never scans the population.
type Selector struct {
	n       int
	indices []int

	mu sync.Mutex
	// accessed counts the population elements touched by the most recent
	// Sample. Unexported on purpose: it is observable only from
	// white-box tests in this package, never through any exported API.
	accessed int
}

// New builds a Selector for population size n, sample size s, offset r.
func New(n, s int, r float64) *Selector {
	return &Selector{n: n, indices: samp.Indices(n, s, r)}
}

// Indices returns a copy of the precomputed sample indices.
func (sl *Selector) Indices() []int {
	out := make([]int, len(sl.indices))
	copy(out, sl.indices)
	return out
}

// Sample returns vals[index_i] for each precomputed index. It touches
// exactly one population element per index. A length mismatch fails
// before any state is touched, leaving the counter untouched.
func (sl *Selector) Sample(vals []int64) ([]int64, error) {
	if len(vals) != sl.n {
		return nil, ErrPopulationLength
	}
	out := make([]int64, 0, len(sl.indices))
	accessed := 0
	for _, ix := range sl.indices {
		out = append(out, vals[ix])
		accessed++
	}
	sl.mu.Lock()
	sl.accessed = accessed
	sl.mu.Unlock()
	return out, nil
}
