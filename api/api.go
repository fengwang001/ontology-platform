// Package api is the public face of the difference-constraints solver.
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/dc"
	"ontology/sp"
)

// Re-exported sentinels; the four failure modes are mutually distinct.
var (
	ErrNonPositiveN     = dc.ErrNonPositiveN
	ErrVarOutOfRange    = dc.ErrVarOutOfRange
	ErrNegativeSelfLoop = dc.ErrNegativeSelfLoop
	ErrInfeasible       = sp.ErrNegativeCycle
)

// Solver holds a registered constraint set; the read paths are concurrency-safe.
type Solver struct {
	sys *dc.System
}

// New fixes the variable count to n; rejects non-positive n.
func New(n int) (*Solver, error) {
	sys, err := dc.New(n)
	if err != nil {
		return nil, err
	}
	return &Solver{sys: sys}, nil
}

// AddConstraint registers x_v - x_u <= w; rejections leave no trace.
func (s *Solver) AddConstraint(u, v int, w int64) error { return s.sys.Add(u, v, w) }

// ConstraintCount returns the number of registered constraints.
func (s *Solver) ConstraintCount() int { return s.sys.Count() }

// Solve returns the pointwise-maximal feasible assignment, or ErrInfeasible.
// It solves on a snapshot in a fresh engine and never mutates the set.
func (s *Solver) Solve() ([]int64, error) {
	return sp.NewEngine(s.sys.N(), s.sys.Snapshot()).Solve()
}

// feasible reports whether x satisfies every constraint x_v - x_u <= w.
func feasible(cons []dc.Constraint, x []int64) bool {
	for _, c := range cons {
		if x[c.V]-x[c.U] > c.W {
			return false
		}
	}
	return true
}

// maximal checks the tightness characterization of the pointwise-maximal
// solution: x_i <= 0, and every x_i < 0 is pinned by a tight constraint.
func maximal(n int, cons []dc.Constraint, x []int64) bool {
	for i := 0; i < n; i++ {
		if x[i] > 0 {
			return false
		}
		tight := x[i] == 0
		for _, c := range cons {
			if !tight && c.V == i && x[c.U]+c.W == x[i] {
				tight = true
			}
		}
		if !tight {
			return false
		}
	}
	return true
}

// naiveSolve is the reference: relax every edge each round until stable.
// Callers must only use it on feasible systems.
func naiveSolve(n int, cons []dc.Constraint) []int64 {
	d := make([]int64, n) // super-source edges x_i <= 0 already applied
	for round := 0; round < n; round++ {
		changed := false
		for _, c := range cons {
			if d[c.U]+c.W < d[c.V] {
				d[c.V] = d[c.U] + c.W
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return d
}

// SelfCheck verifies the four invariants on built-in constraint sequences.
func (*Solver) SelfCheck() error {
	if _, err := New(0); !errors.Is(err, ErrNonPositiveN) { // invariant 4
		return fmt.Errorf("selfcheck: New(0) = %v", err)
	}
	probe, _ := New(2)
	if err := probe.AddConstraint(0, 2, 1); !errors.Is(err, ErrVarOutOfRange) {
		return fmt.Errorf("selfcheck: out-of-range = %v", err)
	}
	if err := probe.AddConstraint(1, 1, -1); !errors.Is(err, ErrNegativeSelfLoop) {
		return fmt.Errorf("selfcheck: self-loop = %v", err)
	}
	if probe.ConstraintCount() != 0 {
		return errors.New("selfcheck: rejected add changed the set")
	}
	cases := []struct {
		n    int
		cons []dc.Constraint
		ok   bool // false: infeasible
	}{
		{3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}}, true},
		{4, []dc.Constraint{{U: 0, V: 1, W: 5}, {U: 1, V: 2, W: -10}, {U: 0, V: 2, W: -6}, {U: 3, V: 0, W: 2}}, true},
		{3, []dc.Constraint{{U: 0, V: 1, W: -2}, {U: 1, V: 2, W: -3}, {U: 2, V: 0, W: -10}}, false},
	}
	for i, c := range cases {
		sv, err := New(c.n)
		if err != nil {
			return err
		}
		for _, e := range c.cons {
			if err := sv.AddConstraint(e.U, e.V, e.W); err != nil {
				return err
			}
		}
		got, err := sv.Solve()
		if !c.ok {
			if !errors.Is(err, ErrInfeasible) || sv.ConstraintCount() != len(c.cons) {
				return fmt.Errorf("selfcheck: case %d: want infeasible, no trace; got %v", i, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("selfcheck: case %d: %w", i, err)
		}
		if !slices.Equal(got, naiveSolve(c.n, c.cons)) { // invariant 3
			return fmt.Errorf("selfcheck: case %d differs from naive reference", i)
		}
		if !feasible(c.cons, got) || !maximal(c.n, c.cons, got) { // invariants 1,2
			return fmt.Errorf("selfcheck: case %d violates feasibility/maximality", i)
		}
	}
	return nil
}
