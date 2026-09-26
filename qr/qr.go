// Package qr drives the Householder QR factorization A = Q·R. It depends only
// on package hh. The factorized Q/R are freshly allocated; Factor never mutates
// its input.
package qr

import (
	"errors"
	"sync/atomic"

	"ontology/hh"
)

// Distinct, decidable sentinel errors (errors.Is works).
var (
	// ErrEmpty: n < 1.
	ErrEmpty = errors.New("qr: empty matrix: n must be >= 1")
	// ErrDimMismatch: len(a) is not n*n.
	ErrDimMismatch = errors.New("qr: dimension mismatch: len(a) must equal n*n")
	// ErrZeroTail: a column tail x = A[k:n][k] is zero from its first entry,
	// so the reflector direction is undefined.
	ErrZeroTail = errors.New("qr: zero column tail: reflector direction undefined")
)

// reflectorCount records the number of reflectors actually constructed across
// Factor calls. It is deliberately unexported and atomic; the public API has
// no accessor for it. Rejected calls never touch it (Store happens only after
// a fully successful factorization).
var reflectorCount atomic.Int64

// Factor returns Q, R with A = Q·R, Q orthogonal and R upper triangular.
// On error no package state changes and returned slices are nil.
func Factor(a []float64, n int) (Q, R []float64, err error) {
	// Validate before allocating or counting anything: a rejected call leaves
	// no trace at all.
	if n < 1 {
		return nil, nil, ErrEmpty
	}
	if len(a) != n*n {
		return nil, nil, ErrDimMismatch
	}

	w := make([]float64, n*n)
	copy(w, a) // input is read-only; all elimination happens on this copy

	// Q accumulator, starting at I. Q = H_0·H_1·...·H_{t-1}.
	q := make([]float64, n*n)
	for i := 0; i < n; i++ {
		q[i*n+i] = 1
	}

	var count int64
	for k := 0; k < n; k++ {
		m := n - k
		x := make([]float64, m)
		for i := 0; i < m; i++ {
			x[i] = w[(k+i)*n+k]
		}
		allZero := true
		for _, xi := range x {
			if xi != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			// Local copy discarded; counter/accumulator are call-local so far.
			return nil, nil, ErrZeroTail
		}
		v, ok := hh.Reflector(x)
		if !ok {
			continue // sub-diagonal already zero: H = I, nothing constructed
		}
		count++

		var vtv float64
		for _, vi := range v {
			vtv += vi * vi
		}
		beta := 2 / vtv

		hh.Apply(v, w, n, k) // R <- H_k·R, columns k..n-1 only
		for i := 1; i < m; i++ {
			w[(k+i)*n+k] = 0 // eliminated entries are exactly zero
		}
		// Q <- Q·H_k: H_k = I - beta·v̂·v̂ᵀ with v̂ embedded at rows k..n-1.
		for r := 0; r < n; r++ {
			var dot float64
			for i := 0; i < m; i++ {
				dot += v[i] * q[r*n+k+i]
			}
			f := beta * dot
			for i := 0; i < m; i++ {
				q[r*n+k+i] -= f * v[i]
			}
		}
	}

	reflectorCount.Store(count) // publish only on full success
	return q, w, nil
}
