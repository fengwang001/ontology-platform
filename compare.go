package ontology

// Comparisons ranks a working copy of rows (treated as one partition) and
// returns the number of comparator invocations sort performed. The input
// is never modified. It lets callers verify sorting stays within an
// O(n log n) comparison budget instead of degrading to O(n^2) scans.
func Comparisons(rows []Row, dir Direction) int {
	work := make([]Row, len(rows))
	copy(work, rows)
	c := &comparisonCounter{}
	rankPartition(work, dir, c)
	return c.Comparisons()
}

// ComparisonBound returns 10*n*ceil(log2(n+1)), the generous O(n log n)
// ceiling used by the test suite for a partition of n accepted rows.
func ComparisonBound(n int) int {
	k := 0
	for (1 << k) < n+1 {
		k++
	}
	return 10 * n * k
}
