// Package pivot selects a partial-pivot row for Gaussian elimination. It
// depends on no other package in the module.
package pivot

import (
	"math"
	"sync/atomic"
)

// scanCounter records the number of column entries scanned *extra* solely to
// decide singularity. The field is unexported; the only exported observation
// is the boolean predicate ExtraScansZero, so the numeric value can never be
// read through the public API (or the demo).
type scanCounter struct {
	extra atomic.Int64
}

var scans = &scanCounter{}

// ExtraScansZero reports whether the accumulated extra-scan count is zero.
// It deliberately exposes only a boolean, never the count itself.
func ExtraScansZero() bool { return scans.extra.Load() == 0 }

// ResetExtraScans restores the counter to zero.
func ResetExtraScans() { scans.extra.Store(0) }

// Pick returns the row r in [k,n) with the largest |a[r*n+k]|; ties resolve
// to the smallest index. ok is false exactly when every candidate entry is
// zero, i.e. column k is singular at and below row k.
//
// Finding the maximum and testing for an all-zero column are fused into one
// single pass, so this correct implementation never performs an extra
// re-scan and the extra-scan counter remains exactly zero.
func Pick(a []float64, n, k int) (row int, ok bool) {
	row = k
	best := math.Abs(a[k*n+k])
	for i := k + 1; i < n; i++ {
		v := math.Abs(a[i*n+k])
		// Strictly greater only: an equal magnitude never replaces the
		// current row, so the smallest index wins every tie.
		if v > best {
			best = v
			row = i
		}
	}
	return row, best != 0
}
