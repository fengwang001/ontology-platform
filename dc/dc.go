// Package dc registers and validates difference constraints x_v - x_u <= w.
// It depends on nothing; invalid registrations are rejected before they
// can touch the registered set.
package dc

import (
	"errors"
	"sync"
)

// Sentinel errors, mutually distinguishable via errors.Is.
var (
	ErrNonPositiveN     = errors.New("dc: variable count must be positive")
	ErrVarOutOfRange    = errors.New("dc: constraint references undefined variable")
	ErrNegativeSelfLoop = errors.New("dc: negative-weight self-loop is always false")
)

// Constraint is x[V] - x[U] <= W, i.e. the directed edge U->V of weight W.
type Constraint struct {
	U, V int
	W    int64
}

// System is a fixed-size set of registered constraints over variables [0, n).
type System struct {
	mu   sync.RWMutex // guards cons; n is immutable after New
	n    int
	cons []Constraint
}

// New rejects non-positive n.
func New(n int) (*System, error) {
	if n <= 0 {
		return nil, ErrNonPositiveN
	}
	return &System{n: n}, nil
}

// N returns the fixed variable count.
func (s *System) N() int { return s.n }

// Add validates first and appends only on success, so a rejected
// constraint never changes the registered set.
func (s *System) Add(u, v int, w int64) error {
	if u < 0 || u >= s.n || v < 0 || v >= s.n {
		return ErrVarOutOfRange
	}
	if u == v && w < 0 {
		return ErrNegativeSelfLoop
	}
	s.mu.Lock()
	s.cons = append(s.cons, Constraint{U: u, V: v, W: w})
	s.mu.Unlock()
	return nil
}

// Count returns the number of registered constraints. Safe for concurrent use.
func (s *System) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cons)
}

// Snapshot returns a copy of the registered constraints, so solvers work
// on a stable view. Safe for concurrent use.
func (s *System) Snapshot() []Constraint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Constraint, len(s.cons))
	copy(out, s.cons)
	return out
}
