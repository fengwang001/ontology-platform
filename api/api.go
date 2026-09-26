// Package api is the public entry point for matrix inversion.
package api

import (
	"errors"
	"math"
	"slices"

	"ontology/gauss"
)

// Sentinel errors, re-exported from gauss; mutually distinguishable.
var (
	ErrEmpty    = gauss.ErrEmpty
	ErrDim      = gauss.ErrDim
	ErrSingular = gauss.ErrSingular
)

// Handle is the public facade. It holds no mutable state.
type Handle struct{}

// New returns a ready-to-use Handle.
func New() *Handle { return &Handle{} }

// Inv returns the inverse of the row-major n*n matrix a.
// a is only read, never modified; concurrent calls are safe.
func (h *Handle) Inv(a []float64, n int) ([]float64, error) {
	return gauss.Invert(a, n)
}

// SelfCheck verifies the four invariants on built-in matrices and
// returns the first violation as an error, nil when all hold.
func (h *Handle) SelfCheck() error {
	// Invariant 1+2: A*inv == I, and exact inverse where representable.
	m2 := []float64{0, 2, 1, 1}
	inv2, err := h.Inv(m2, 2)
	if err != nil {
		return err
	}
	if !slices.Equal(inv2, []float64{-0.5, 1, 0.5, 0}) { // exact
		return errors.New("api: exact inverse of built-in 2x2 mismatch")
	}
	m4 := []float64{ // 可逆，含负值与零
		0, -1, 2, 0,
		3, 0, 0, 1,
		0, 2, -1, 0,
		1, 0, 0, -2,
	}
	inv4, err := h.Inv(m4, 4)
	if err != nil {
		return err
	}
	if maxErrI(m4, inv4, 4) > 1e-9 {
		return errors.New("api: A*inv(A) deviates from I beyond 1e-9")
	}
	// Invariant 3: input not modified.
	before := slices.Clone(m4)
	if _, err := h.Inv(m4, 4); err != nil || !slices.Equal(m4, before) {
		return errors.New("api: input matrix modified by Inv")
	}
	// Invariant 4: rejections leave no trace and are distinguishable.
	if _, err := h.Inv(make([]float64, 3), 2); !errors.Is(err, ErrDim) {
		return errors.New("api: dimension mismatch not reported as ErrDim")
	}
	if _, err := h.Inv(nil, 0); !errors.Is(err, ErrEmpty) {
		return errors.New("api: empty matrix not reported as ErrEmpty")
	}
	if _, err := h.Inv([]float64{1, 2, 2, 4}, 2); !errors.Is(err, ErrSingular) {
		return errors.New("api: singular matrix not reported as ErrSingular")
	}
	again, err := h.Inv(m2, 2) // still fully usable afterwards
	if err != nil || !slices.Equal(again, inv2) {
		return errors.New("api: state changed after rejected calls")
	}
	return nil
}

func maxErrI(a, inv []float64, n int) float64 {
	m := 0.0
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			sum := 0.0
			for k := 0; k < n; k++ {
				sum += a[i*n+k] * inv[k*n+j]
			}
			want := 0.0
			if i == j {
				want = 1
			}
			if d := math.Abs(sum - want); d > m {
				m = d
			}
		}
	}
	return m
}
