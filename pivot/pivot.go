// Package pivot selects pivot rows for Gaussian elimination with partial pivoting.
package pivot

import "math"

// Pick returns the index of the row in [k,n) whose k-th column has the largest
// absolute value. Ties go to the smallest index: the scan starts at k and the
// best row is replaced only on a strictly larger magnitude. If every candidate
// is zero (or the range is empty), ok is false.
func Pick(a []float64, n, k int) (row int, ok bool) {
	best := k
	bestAbs := 0.0
	found := false
	for i := k; i < n; i++ {
		v := math.Abs(a[i*n+k])
		if v > bestAbs {
			bestAbs = v
			best = i
			found = true
		}
	}
	return best, found
}
