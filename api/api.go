// Package api is the external entry point of the determinant subsystem.
package api

import (
	"errors"
	"math"

	"ontology/elim"
)

// API holds the in-process state of the determinant subsystem. It is stateless
// from the caller's view; elimination state lives in the elim package.
type API struct{}

// New returns a ready API.
func New() *API { return &API{} }

// Det returns the determinant of the n×n row-major matrix a.
func (x *API) Det(a []float64, n int) (float64, error) {
	return elim.Det(a, n)
}

// naiveDet is the recursive definition by expansion along the first row,
// used only as the reference oracle for SelfCheck.
func naiveDet(a []float64, n int) float64 {
	if n == 1 {
		return a[0]
	}
	d := 0.0
	for j := 0; j < n; j++ {
		sub := make([]float64, 0, (n-1)*(n-1))
		for i := 1; i < n; i++ {
			for c := 0; c < n; c++ {
				if c != j {
					sub = append(sub, a[i*n+c])
				}
			}
		}
		term := a[j] * naiveDet(sub, n-1)
		if j%2 == 0 {
			d += term
		} else {
			d -= term
		}
	}
	return d
}

// builtinMatrices is the fixed set used by SelfCheck: n=1, negatives, zeros,
// a singular matrix, even/odd row swaps and the n=3 worked example.
func builtinMatrices() [][]float64 {
	return [][]float64{
		{7},
		{-3},
		{0, 1, 1, 0},
		{0, 1, 1, 1, 0, 1, 1, 1, 0},
		{1, 0, 0, 0, 1, 0, 0, 0, 1},
		{0, 1, 0, 0, 0, 1, 1, 0, 0}, // 3-cycle: two swaps -> sign stays +
		{2, -1, 0, -1, 2, -1, 0, -1, 2},
		{1, 2, 3, 2, 4, 6, 7, 8, 9}, // singular (rows 0,1 proportional)
		{-2, 3, 1, 0, 4, -5, 2, -1, 6, 0, -3, 2, 1, 1, -4, 1},
	}
}

// SelfCheck verifies the four invariants over built-in matrices plus the
// complexity and failure-no-trace properties. It returns nil iff all pass.
func (x *API) SelfCheck() error {
	for _, a := range builtinMatrices() {
		n := int(math.Sqrt(float64(len(a))))
		snapshot := append([]float64(nil), a...)
		got, err := elim.Det(a, n)
		if err != nil {
			return err
		}
		if math.Abs(got-naiveDet(snapshot, n)) > 1e-9 {
			return errors.New("api: determinant disagrees with naive expansion")
		}
		for i := range a {
			if a[i] != snapshot[i] {
				return errors.New("api: Det modified its input")
			}
		}
	}
	if err := elim.VerifyRejectionNoTrace(); err != nil {
		return err
	}
	return elim.VerifyTriangularNoOps(100, 1000, 10000)
}
