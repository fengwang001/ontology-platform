// Package hh constructs and applies Householder reflectors. It depends on no
// other package in this module (dependency direction: api -> qr -> hh).
package hh

import "math"

// Reflector returns the normalized Householder vector v (v[0] == 1) for a
// column tail x = A[k:n][k], following
//
//	v = x - sign(x[0])*||x||*e_1,  sign(0) = +1,  then v <- v/v[0].
//
// ok is false when the sub-diagonal part x[1:] is already all zero: that
// column needs no reflector (H = I) and no v is constructed.
func Reflector(x []float64) (v []float64, ok bool) {
	if len(x) <= 1 {
		return nil, false
	}
	for i := 1; i < len(x); i++ {
		if x[i] != 0 {
			goto need
		}
	}
	return nil, false
need:
	var norm float64
	for _, xi := range x {
		norm += xi * xi
	}
	norm = math.Sqrt(norm)
	sign := 1.0
	if x[0] < 0 {
		sign = -1.0
	}
	v = make([]float64, len(x))
	v[0] = x[0] - sign*norm
	for i := 1; i < len(x); i++ {
		v[i] = x[i]
	}
	// Mathematically unreachable when x[1:] has a nonzero entry; guard keeps
	// extreme-scale rounding (v0 == 0) from producing inf/NaN.
	if v[0] == 0 {
		return nil, false
	}
	inv := 1 / v[0]
	for i := range v {
		v[i] *= inv
	}
	return v, true
}

// Apply applies H = I - beta*v*vᵀ with beta = 2/(vᵀv) to columns k..n-1 of
// the n×n row-major matrix a. Columns 0..k-1 are never read or written.
// v has length n-k and is normalized (v[0] == 1).
func Apply(v []float64, a []float64, n, k int) {
	m := len(v)
	var vtv float64
	for _, vi := range v {
		vtv += vi * vi
	}
	beta := 2 / vtv
	for j := k; j < n; j++ {
		var dot float64
		for i := 0; i < m; i++ {
			dot += v[i] * a[(k+i)*n+j]
		}
		f := beta * dot
		for i := 0; i < m; i++ {
			a[(k+i)*n+j] -= f * v[i]
		}
	}
}
