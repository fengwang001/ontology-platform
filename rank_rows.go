package ontology

// RankRows ranks rows per partition and returns all three columns.
//
// Rows with a nil partition key or a NaN value are rejected and counted
// in Result.Stats. Accepted rows are grouped by partition, sorted by
// value (then by ID ascending inside ties), and returned with partitions
// in lexicographic order. The input slice and its elements are never
// modified, and the returned slice is freshly allocated.
func RankRows(rows []InputRow, opts Options) Result {
	groups, stats := validateAndGroup(rows)
	keys := sortedPartitions(groups)

	total := 0
	for _, key := range keys {
		total += len(groups[key])
	}
	out := make([]RankedRow, 0, total)
	for _, key := range keys {
		items := groups[key]
		mergeSortItems(items, opts.Order)
		out = append(out, assignRanks(key, items)...)
	}
	return Result{Rows: out, Stats: stats}
}

// MaxComparisons is the guaranteed upper bound on value-order
// comparisons for one partition of n rows: 10*n*ceil(log2(n+1)).
func MaxComparisons(n int) int {
	if n < 0 {
		panic("ontology: negative row count")
	}
	return 10 * n * ceilLog2(n+1)
}

func ceilLog2(n int) int {
	if n <= 1 {
		return 0
	}
	k := 0
	for v := 1; v < n; v <<= 1 {
		k++
	}
	return k
}

// SortPartitionComparisons ranks one partition and returns the number
// of ordering comparisons performed, so callers can assert the bound.
func SortPartitionComparisons(items []InputRow, opts Options) (Result, int) {
	if len(items) == 0 {
		return RankRows(items, opts), 0
	}
	grouped, stats := validateAndGroup(items)
	if len(grouped) != 1 {
		return RankRows(items, opts), 0
	}
	var only []rankedItem
	for _, group := range grouped {
		only = group
	}
	comparisons := mergeSortItems(only, opts.Order)
	ranked := assignRanks("", only)
	if stats.SkippedNilPartition != 0 || stats.SkippedNaN != 0 {
		ranked = nil
	}
	return Result{Rows: ranked, Stats: stats}, comparisons
}
