package ontology

import "math"

// lessFunc reports whether a sorts strictly before b. A nil counter means
// comparisons are not counted.
type lessFunc func(a, b sortedRow) bool

// rankWithLess is the internal entry point. injectLess, when non-nil,
// replaces the comparator used for ordering, so tests can count comparisons.
func rankWithLess(rows []Row, dir Direction, injectLess func(counter *int) lessFunc) RankResult {
	groups := map[string][]sortedRow{}
	skipped := 0

	for _, src := range rows {
		if src.Partition == nil || math.IsNaN(src.Value) {
			skipped++
			continue
		}
		// Copy into a fresh backing array: sorting must never reorder or
		// mutate the caller's slice / Row values.
		groups[*src.Partition] = append(groups[*src.Partition], sortedRow{
			partition: *src.Partition,
			value:     src.Value,
			id:        src.ID,
		})
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	// Partition keys are plain strings, so lexicographic byte order is exact.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}

	out := make([]RankedRow, 0, len(rows)-skipped)
	for _, key := range keys {
		out = append(out, rankPartition(groups[key], dir, injectLess)...)
	}
	return RankResult{Rows: out, Skipped: skipped}
}

// sortedRow is an owned, internal copy of an input Row.
type sortedRow struct {
	partition string
	value     float64
	id        string
}
