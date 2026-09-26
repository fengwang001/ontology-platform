// Package pivot selects a partial-pivot row for Gauss-Jordan elimination.
package pivot

import "math"

// Pick returns the row in [k,n) whose entry in column k has the largest
// absolute value in the row-major n×n matrix a. On a tie the row with the
// smallest index is chosen. ok is false when every candidate entry is zero.
func Pick(a []float64, n, k int) (row int, ok bool) {
	best := k
	bestAbs := math.Abs(a[k*n+k])
	for i := k + 1; i < n; i++ {
		v := math.Abs(a[i*n+k])
		if v > bestAbs { // strict comparison keeps the smallest index on ties
			bestAbs = v
			best = i
		}
	}
	if bestAbs == 0 {
		return 0, false
	}
	return best, true
}
