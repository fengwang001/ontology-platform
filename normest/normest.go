// Package normest estimates ||A⁻¹||∞ with a fixed number of solves over a
// single LU factorization; A⁻¹ is never formed. Depends only on norm.
package normest

import (
	"errors"
	"sync/atomic"

	"ontology/norm"
)

// ErrBadK is the fourth, distinct failure class: k must be at least 1.
var ErrBadK = errors.New("normest: illegal k (k < 1)")

// Unexported counters: only same-package white-box tests read them; they
// never appear in any exported signature or return value.
var (
	solves  atomic.Int64 // A·y=b solves actually performed
	factors atomic.Int64 // LU factorizations actually performed
)

type round struct {
	b, y, sign []float64
	yInf       float64
}

// EstimateInvInf estimates ||A⁻¹||∞: from b=[1,…,1], exactly k solves over
// one shared LU factorization, taking the largest ‖y‖∞, b ← sign(y).
func EstimateInvInf(a []float64, n, k int) (float64, error) {
	est, _, err := estimate(a, n, k, false)
	return est, err
}

func estimate(a []float64, n, k int, keepTrace bool) (float64, []round, error) {
	// All validation precedes factorization and any counter change, so a
	// rejected call leaves no state behind.
	if n < 1 {
		return 0, nil, norm.ErrEmpty
	}
	if len(a) != n*n {
		return 0, nil, norm.ErrDim
	}
	if k < 1 {
		return 0, nil, ErrBadK
	}
	L, U, err := norm.Factor(a, n)
	if err != nil {
		return 0, nil, err // singular: counters untouched
	}
	factors.Add(1)
	b := make([]float64, n)
	for i := range b {
		b[i] = 1
	}
	var rounds []round
	if keepTrace {
		rounds = make([]round, 0, k)
	}
	best := 0.0
	for r := 0; r < k; r++ {
		y := norm.Solve(L, U, n, b) // substitution only, never an inverse
		solves.Add(1)
		yInf := 0.0
		for _, v := range y {
			if v < 0 {
				v = -v
			}
			if v > yInf {
				yInf = v
			}
		}
		if yInf > best {
			best = yInf
		}
		s := make([]float64, n)
		for i, v := range y {
			if v > 0 {
				s[i] = 1
			} else if v < 0 {
				s[i] = -1
			}
		}
		if keepTrace {
			rounds = append(rounds, round{
				append([]float64(nil), b...),
				append([]float64(nil), y...), s, yInf,
			})
		}
		b = s
	}
	return best, rounds, nil
}

// SelfCheck verifies internal properties invisible across packages: exactly
// 3 solves and 1 factorization for n=100/1000/10000, and the exact n=2
// round trace. Counter values are never exposed.
func SelfCheck() error {
	for _, n := range []int{100, 1000, 10000} {
		a := make([]float64, n*n) // upper-bidiagonal, no zero pivot
		for i := 0; i < n; i++ {
			a[i*n+i] = 2
			if i+1 < n {
				a[i*n+i+1] = 1
			}
		}
		s0, f0 := solves.Load(), factors.Load()
		if _, err := EstimateInvInf(a, n, 3); err != nil {
			return err
		}
		if d := solves.Load() - s0; d != 3 {
			return errors.New("normest: solve count not equal to k")
		}
		if d := factors.Load() - f0; d != 1 {
			return errors.New("normest: factorization count not equal to 1")
		}
	}
	est, rs, err := estimate([]float64{1, 3, 0, 2}, 2, 2, true)
	if err != nil {
		return err
	}
	want := []round{
		{[]float64{1, 1}, []float64{-0.5, 0.5}, []float64{-1, 1}, 0.5},
		{[]float64{-1, 1}, []float64{-2.5, 0.5}, []float64{-1, 1}, 2.5},
	}
	if est != 2.5 || len(rs) != len(want) {
		return errors.New("normest: n=2 estimate mismatch")
	}
	for i := range want {
		if !vecEq(rs[i].b, want[i].b) || !vecEq(rs[i].y, want[i].y) ||
			!vecEq(rs[i].sign, want[i].sign) || rs[i].yInf != want[i].yInf {
			return errors.New("normest: n=2 round trace mismatch")
		}
	}
	return nil
}

func vecEq(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
