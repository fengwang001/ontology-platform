package ontology

// CompareFloat is the default ascending-order comparator for sort values.
//
// NaN is not expected here (such rows are rejected by Rank); if it does
// appear it orders before every other value. +0.0 and -0.0 compare equal,
// and the infinities order naturally at the two ends.
func CompareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		// Covers +0.0 == -0.0 and NaN == NaN.
		return 0
	}
}

// ceilLog2PlusOne returns ceil(log2(n+1)) for n >= 0.
// It is unexported test support for the comparison-count bound.
func ceilLog2PlusOne(n int) int {
	k := 1 // log2(n+1) is at least 1 once n >= 1
	power := 2
	for power < n+1 {
		power <<= 1
		k++
	}
	return k
}

// ComparisonBound returns the maximum number of value comparisons allowed
// for one partition of n rows: 10*n*ceil(log2(n+1)).
func ComparisonBound(n int) int {
	return 10 * n * ceilLog2PlusOne(n)
}
