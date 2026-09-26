// Package pivot implements partial-pivot row selection for Gaussian
// elimination over row-major n*n float64 matrices.
package pivot

import "math"

// Pick returns the row in [k, n) whose column-k entry has the largest
// absolute value in a. Ties are broken by the smallest row index.
// ok is false when every candidate entry in column k is exactly zero
// (the matrix is singular at step k).
func Pick(a []float64, n, k int) (row int, ok bool) {
	row, best := -1, 0.0
	for i := k; i < n; i++ {
		if v := math.Abs(a[i*n+k]); row < 0 || v > best {
			row, best = i, v
		}
	}
	if row < 0 || best == 0 {
		return 0, false
	}
	return row, true
}
