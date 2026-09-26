// Package hh constructs and applies Householder reflectors.
// It depends on no other package.
package hh

import "math"

// Reflector builds the normalized Householder vector for tail vector x:
//
//	v = (x - sign(x[0])*||x||*e1) / (x[0] - sign(x[0])*||x||),  v[0] == 1
//
// so that H = I - 2 v v^T/(v^T v) zeros x[1:].
// ok is false (v == nil) when x[1:] is already entirely zero: no reflector
// is needed for that column. sign(0) == +1.
func Reflector(x []float64) (v []float64, ok bool) {
	m := len(x)
	if m <= 1 {
		return nil, false
	}
	bottomZero := true
	for i := 1; i < m; i++ {
		if x[i] != 0 {
			bottomZero = false
			break
		}
	}
	if bottomZero {
		return nil, false
	}
	var norm float64
	for _, xi := range x {
		norm += xi * xi
	}
	norm = math.Sqrt(norm)
	sign := 1.0
	if x[0] < 0 {
		sign = -1.0
	}
	v0 := x[0] - sign*norm // != 0: x[1:] nonzero implies ||x|| > |x[0]|
	v = make([]float64, m)
	for i := 0; i < m; i++ {
		v[i] = x[i] / v0
	}
	v[0] = 1.0 // enforce exactly, guarding against rounding
	return v, true
}

// Apply applies H = I - 2 v v^T/(v^T v) in place to columns k..n-1 of the
// n x n row-major matrix a, restricted to rows k..n-1 (len(v) == n-k).
// Columns 0..k-1 are never touched.
func Apply(v, a []float64, n, k int) {
	m := len(v)
	var vv float64
	for _, vi := range v {
		vv += vi * vi
	}
	beta := 2.0 / vv
	for j := k; j < n; j++ {
		var dot float64
		base := k*n + j
		for i := 0; i < m; i++ {
			dot += v[i] * a[base+i*n]
		}
		tau := beta * dot
		for i := 0; i < m; i++ {
			a[base+i*n] -= tau * v[i]
		}
	}
}
