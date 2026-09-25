package ontology

import "math"

// comparisonCounter counts value comparisons made while sorting.
type comparisonCounter struct {
	count int
}

// compare returns -1/0/1 for a below/equal/above b. NaN is never passed in
// because NaN rows are rejected up front; +0.0 and -0.0 compare equal.
func (c *comparisonCounter) compare(a, b float64) int {
	c.count++
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// equalValues treats +0.0 and -0.0 as equal; NaN is never passed in.
func equalValues(a, b float64) bool {
	if a == b {
		return true
	}
	return a == 0 && b == 0
}

// invalidRow reports rows that must be skipped: a missing partition key or a
// NaN sort value.
func invalidRow(r Row) bool {
	return r.Partition == nil || math.IsNaN(r.Value)
}

// maxComparisons is the O(n log n) budget the tests enforce.
func maxComparisons(n int) int {
	return 10 * n * ceilLog2(n+1)
}

func ceilLog2(n int) int {
	if n <= 1 {
		return 0
	}
	k := 0
	for (1 << k) < n {
		k++
	}
	return k
}
