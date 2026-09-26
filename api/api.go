// Package api is the external entry point for solving A x = b. It depends
// only on elim and pivot; the dependency direction api -> elim -> pivot is
// one-way and never reversed.
package api

import (
	"fmt"
	"math"

	"ontology/elim"
	"ontology/pivot"
)

// API is the stateless solver handle. All state lives in process memory and
// there is nothing to mutate, so a rejected operation cannot leave a trace.
type API struct{}

// New constructs a solver handle.
func New() *API { return &API{} }

// Solve solves A x = b for the n*n row-major matrix a and length-n vector b.
// Inputs are read-only; a copy is used internally.
func (s *API) Solve(a, b []float64, n int) ([]float64, error) {
	return elim.Solve(a, b, n)
}

// system is one built-in non-singular test case with its known solution.
type system struct {
	a, b, want []float64
	n          int
}

// builtins exercises n=1, identity, negative entries and a pivot-tie case.
func builtins() []system {
	return []system{
		{[]float64{2}, []float64{6}, []float64{3}, 1},
		{[]float64{1, 0, 0, 0, 1, 0, 0, 0, 1}, []float64{1, 2, 3}, []float64{1, 2, 3}, 3},
		{[]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, []float64{5, 4, 3}, []float64{1, 2, 3}, 3},
		{[]float64{-2, 1, 1, -2}, []float64{0, -3}, []float64{1, 2}, 2},
		// Column 0 magnitudes [1,1] tie: the smallest row must win.
		{[]float64{1, 2, 1, 3}, []float64{3, 5}, []float64{-1, 2}, 2},
	}
}

// SelfCheck verifies the four invariants of section 2 against a built-in set
// of systems and returns a non-nil, decidable error on the first violation.
func (s *API) SelfCheck() error {
	for _, t := range builtins() {
		a0, b0 := append([]float64(nil), t.a...), append([]float64(nil), t.b...)
		x, err := elim.Solve(a0, b0, t.n)
		if err != nil {
			return fmt.Errorf("selfcheck: unexpected error %w", err)
		}
		// Invariant 1: residual |A*x - b| <= 1e-9 entry by entry.
		for i := 0; i < t.n; i++ {
			d := -b0[i]
			for j := 0; j < t.n; j++ {
				d += a0[i*t.n+j] * x[j]
			}
			if math.Abs(d) > 1e-9 {
				return fmt.Errorf("selfcheck: residual %v exceeds 1e-9", d)
			}
		}
		// Invariant 2: inputs are byte-identical after Solve.
		for i := range a0 {
			if a0[i] != t.a[i] {
				return fmt.Errorf("selfcheck: matrix input mutated")
			}
		}
		for i := range b0 {
			if b0[i] != t.b[i] {
				return fmt.Errorf("selfcheck: vector input mutated")
			}
		}
	}

	// Invariant 3: a tied maximum always resolves to the smallest row and is
	// deterministic across repeated calls. For n=2, column 0 is [1,1].
	tie := []float64{1, 2, 1, 3}
	r1, ok1 := pivot.Pick(tie, 2, 0)
	r2, _ := pivot.Pick(tie, 2, 0)
	if !ok1 || r1 != 0 || r2 != r1 {
		return fmt.Errorf("selfcheck: pivot tie must pick smallest row deterministically, got %d %d ok=%v", r1, r2, ok1)
	}

	// Invariant 4: rejected operations leave no trace: after the three
	// distinct rejections a normal solve still returns the known answer.
	if _, err := elim.Solve(nil, nil, 0); err != elim.ErrEmpty {
		return fmt.Errorf("selfcheck: expected ErrEmpty, got %v", err)
	}
	if _, err := elim.Solve([]float64{1, 2}, []float64{1}, 1); err != elim.ErrDimension {
		return fmt.Errorf("selfcheck: expected ErrDimension, got %v", err)
	}
	if _, err := elim.Solve([]float64{1, 0, 0, 0}, []float64{1, 2}, 2); err != elim.ErrSingular {
		return fmt.Errorf("selfcheck: expected ErrSingular, got %v", err)
	}
	x, err := elim.Solve([]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, []float64{5, 4, 3}, 3)
	if err != nil || !(x[0] == 1 && x[1] == 2 && x[2] == 3) {
		return fmt.Errorf("selfcheck: solver unusable after rejections: %v %v", x, err)
	}
	return nil
}
