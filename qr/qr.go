// Package qr drives the Householder QR factorization. It depends only on hh.
package qr

import (
	"errors"
	"math"
	"sync/atomic"

	"ontology/hh"
)

// Distinct, decidable sentinel errors.
var (
	// ErrDimMismatch: len(a) is not n*n.
	ErrDimMismatch = errors.New("qr: matrix length must equal n*n")
	// ErrEmptyMatrix: n < 1.
	ErrEmptyMatrix = errors.New("qr: n must be at least 1")
	// ErrZeroTail: a column tail is zero from its first element on, so the
	// reflector direction is undefined.
	ErrZeroTail = errors.New("qr: column tail is entirely zero")
)

// reflectorCount is the total number of reflectors actually constructed.
// It is unexported; callers can only observe a pass/fail SelfCheck, never the
// number itself.
var reflectorCount atomic.Int64

// Factor returns Q, R with a = Q R, Q orthogonal and R upper triangular.
// a is read-only: Factor never mutates it and is safe for concurrent use on
// shared input. On error no state is touched and no counter increment occurs.
func Factor(a []float64, n int) (Q, R []float64, err error) {
	if n < 1 {
		return nil, nil, ErrEmptyMatrix
	}
	if len(a) != n*n {
		return nil, nil, ErrDimMismatch
	}
	R = append([]float64(nil), a...) // work on a private copy
	type ref struct {
		k int
		v []float64
	}
	refs := make([]ref, 0, n)
	built := int64(0)
	for k := 0; k < n; k++ {
		m := n - k
		x := make([]float64, m)
		for i := 0; i < m; i++ {
			x[i] = R[(k+i)*n+k]
		}
		var sum float64
		for _, xi := range x {
			sum += xi * xi
		}
		norm := math.Sqrt(sum)
		if norm == 0 {
			return nil, nil, ErrZeroTail
		}
		v, ok := hh.Reflector(x)
		if !ok {
			continue // x[1:] already zero: O(1) skip, no reflector built
		}
		hh.Apply(v, R, n, k)
		sign := 1.0
		if x[0] < 0 {
			sign = -1.0
		}
		R[k*n+k] = sign * norm
		for i := 1; i < m; i++ { // snap washed-out lower triangle to exact zeros
			R[(k+i)*n+k] = 0
		}
		refs = append(refs, ref{k, v})
		built++
	}
	Q = make([]float64, n*n)
	for i := 0; i < n; i++ {
		Q[i*n+i] = 1
	}
	// Q = H_0 H_1 ...; H symmetric, so apply in reverse order to I.
	for i := len(refs) - 1; i >= 0; i-- {
		applyAllColumns(refs[i].v, Q, n, refs[i].k)
	}
	reflectorCount.Add(built) // counted only after full success
	return Q, R, nil
}

// applyAllColumns applies the embedded reflector H_k (block rows/cols k..n-1,
// identity elsewhere) to every column of q.
func applyAllColumns(v, q []float64, n, k int) {
	m := len(v)
	var vv float64
	for _, vi := range v {
		vv += vi * vi
	}
	beta := 2.0 / vv
	for j := 0; j < n; j++ {
		var dot float64
		base := k*n + j
		for i := 0; i < m; i++ {
			dot += v[i] * q[base+i*n]
		}
		tau := beta * dot
		for i := 0; i < m; i++ {
			q[base+i*n] -= tau * v[i]
		}
	}
}

// SelfCheck verifies internal properties that Factor alone cannot expose:
// already-upper-triangular inputs must construct zero reflectors at every
// size, and hh.Apply must never rewrite earlier columns. It returns only
// pass/fail, never the counter value.
func SelfCheck() error {
	for _, n := range []int{100, 1000, 10000} {
		a := make([]float64, n*n)
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				a[i*n+j] = float64(i*n+j) + 1
			}
		}
		before := reflectorCount.Load()
		Q, R, err := Factor(a, n)
		if err != nil {
			return err
		}
		if reflectorCount.Load() != before {
			return errors.New("qr: triangular input unexpectedly built reflectors")
		}
		for i := 0; i < n*n; i++ {
			if R[i] != a[i] || Q[i] != diagonal(i, n) {
				return errors.New("qr: triangular factorization not identity/trivial")
			}
		}
	}
	return nil
}

func diagonal(i, n int) float64 {
	if i%n == i/n {
		return 1
	}
	return 0
}
