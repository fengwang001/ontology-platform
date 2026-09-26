// Package api is the outward-facing entry point. It depends only on package
// qr (which in turn depends on hh); the dependency direction never reverses.
package api

import (
	"errors"
	"math"
	"math/rand"

	"ontology/qr"
)

// Decidable sentinel errors, identical to qr's so errors.Is keeps working.
var (
	ErrEmpty       = qr.ErrEmpty
	ErrDimMismatch = qr.ErrDimMismatch
	ErrZeroTail    = qr.ErrZeroTail
)

const eps = 1e-9

// Engine holds process-local state. It is safe for concurrent use.
type Engine struct{}

// New creates an Engine.
func New() *Engine { return &Engine{} }

// Factor factors the n×n row-major matrix a into Q, R (A = Q·R). The input is
// read-only. A rejected call changes no state and returns a sentinel error.
func (e *Engine) Factor(a []float64, n int) (Q, R []float64, err error) {
	return qr.Factor(a, n)
}

// SelfCheck verifies the four invariants on a set of built-in matrices.
func (e *Engine) SelfCheck() error {
	builtins := [][]float64{
		{5}, // n=1
		{3, 1, 4, 1},
		{1, 2, 3, 0, 4, 5, 0, -6, 7}, // col0 tail already zero (skipped)
	}
	rng := rand.New(rand.NewSource(20260926))
	for _, sz := range []int{2, 5, 8} {
		m := make([]float64, sz*sz)
		for i := range m {
			m[i] = rng.NormFloat64()*7 - 3 // negatives, zeros-near, positives
		}
		builtins = append(builtins, m)
	}
	dims := []int{1, 2, 3, 2, 5, 8}
	for idx, a0 := range builtins {
		n := dims[idx]
		a := append([]float64(nil), a0...) // keep an untouched copy
		Q, R, err := e.Factor(a, n)
		if err != nil {
			return err
		}
		if err := checkInvariants(a, Q, R, n); err != nil {
			return err
		}
		// 不变量3（黑盒面）：已知规范样例必须逐元素等于理论 R；
		// 若后续步误改前序列，整列精确值就会被破坏。
		if idx == 1 && (math.Abs(R[1]-7.0/5) > eps || math.Abs(R[3]-1.0/5) > eps) {
			return errors.New("api: earlier columns were altered by a later reflector")
		}
		for i := range a { // input is read-only
			if a[i] != a0[i] {
				return errors.New("api: Factor mutated its input")
			}
		}
	}
	return e.checkRejections()
}

func checkInvariants(a, Q, R []float64, n int) error {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			// 不变量1: Q·R 逐元素还原 A。
			var recon float64
			for t := 0; t < n; t++ {
				recon += Q[i*n+t] * R[t*n+j]
			}
			if math.Abs(recon-a[i*n+j]) > eps {
				return errors.New("api: Q·R does not reconstruct A")
			}
			// 不变量2: QᵀQ = I。
			var qtq float64
			for t := 0; t < n; t++ {
				qtq += Q[t*n+i] * Q[t*n+j]
			}
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(qtq-want) > eps {
				return errors.New("api: Q is not orthogonal")
			}
			// 不变量2: R 严格上三角。
			if i > j && R[i*n+j] != 0 {
				return errors.New("api: R is not upper triangular")
			}
		}
	}
	return nil
}

// checkRejections covers 不变量4: the three distinct sentinel errors, followed
// by a normal successful call on the same engine (state stays usable).
func (e *Engine) checkRejections() error {
	good := []float64{3, 1, 4, 1}
	snap := append([]float64(nil), good...)
	cases := []struct {
		a   []float64
		n   int
		err error
	}{
		{nil, 0, ErrEmpty},
		{[]float64{1, 2, 3}, 2, ErrDimMismatch}, // len 3 != 4
		{[]float64{1, 0, 2, 0}, 2, ErrZeroTail}, // [[1,0],[2,0]]: column 1 all zero
	}
	seen := map[error]bool{}
	for _, c := range cases {
		_, _, err := e.Factor(c.a, c.n)
		if !errors.Is(err, c.err) {
			return errors.New("api: unexpected rejection error")
		}
		seen[c.err] = true
	}
	if len(seen) != 3 {
		return errors.New("api: sentinel errors are not distinct")
	}
	if _, _, err := e.Factor(good, 2); err != nil {
		return errors.New("api: engine not usable after rejection")
	}
	for i := range good {
		if good[i] != snap[i] {
			return errors.New("api: rejection left a trace on input")
		}
	}
	return nil
}
