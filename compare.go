package rank

import "math"

// cmpCounted compares two rows and records every comparison performed.
//
// Ordering is by sort value (ascending or descending). Rows with equal
// sort values — including +0.0 and -0.0, which compare equal here — are
// broken by row ID in *ascending* order regardless of the value order, so
// ties are deterministic and never depend on slice index or map order.
//
// Returns -1, 0, or 1 like the classic comparison functions.
type cmpCounted struct {
	order Order
	count *int64
}

func (c cmpCounted) Compare(a, b Row) int {
	*c.count++
	if cmp := cmpFloat(a.Value, b.Value); cmp != 0 {
		if c.order == Desc {
			return -cmp
		}
		return cmp
	}
	return cmpString(a.ID, b.ID)
}

// cmpFloat compares two float64 sort values. NaN must never reach this
// function (NaN rows are rejected up front); it is ordered first only as a
// defensive total ordering. +0.0 and -0.0 compare equal.
func cmpFloat(a, b float64) int {
	switch {
	case math.IsNaN(a) || math.IsNaN(b):
		if math.IsNaN(a) && math.IsNaN(b) {
			return 0
		}
		if math.IsNaN(a) {
			return -1
		}
		return 1
	case a == b:
		// Covers +0.0 == -0.0: they form a tie.
		return 0
	case a < b:
		return -1
	default:
		return 1
	}
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// MaxComparisons is the allowed upper bound for comparisons performed on a
// single partition of n rows: 10*n*ceil(log2(n+1)).
func MaxComparisons(n int) int64 {
	if n <= 0 {
		return 0
	}
	return int64(10 * n * ceilLog2(n+1))
}

func ceilLog2(n int) int {
	k := 0
	for (1 << k) < n {
		k++
	}
	return k
}
