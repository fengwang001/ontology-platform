package rank

import (
	"math"
	"sort"
)

// Compute calculates ROW_NUMBER, RANK and DENSE_RANK for every
// accepted row, grouped by partition.
//
// Semantics per partition (1-based):
//   - RowNumber: position in sort order; ties are ordered by row ID
//     ascending, so the result is deterministic.
//   - Rank: 1 + number of rows sorted strictly earlier; tied rows
//     share the same rank and the next distinct value skips ahead.
//   - DenseRank: 1 + number of distinct values sorted strictly
//     earlier; tied rows share the same rank and no numbers are
//     skipped.
//
// Rows with a nil partition key or a NaN sort value are rejected and
// counted in Result.Skipped. The input slice and the Row values in it
// are never modified; all returned slices are freshly allocated.
func Compute(rows []Row, opts Options) Result {
	cmp := newComparator(opts)
	byPartition := make(map[string][]Row)
	var res Result

	for _, r := range rows {
		if r.Partition == nil {
			res.Skipped++
			res.SkippedNilPartition++
			continue
		}
		if math.IsNaN(r.Value) {
			res.Skipped++
			res.SkippedNaN++
			continue
		}
		key := *r.Partition
		byPartition[key] = append(byPartition[key], r)
	}

	keys := make([]string, 0, len(byPartition))
	for key := range byPartition {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	res.Partitions = make([]PartitionResult, 0, len(keys))
	for _, key := range keys {
		group := byPartition[key]
		// Sort a copy so the caller's backing array is untouched.
		sorted := make([]Row, len(group))
		copy(sorted, group)
		cmp.sortRows(sorted)
		res.Partitions = append(res.Partitions, PartitionResult{
			Key:  key,
			Rows: rankSorted(cmp, sorted),
		})
	}
	return res
}

// rankSorted assigns the three ranking values to an already sorted
// partition in a single pass.
func rankSorted(cmp comparator, sorted []Row) []RankedRow {
	ranked := make([]RankedRow, len(sorted))
	rank, dense := 0, 0
	for i, r := range sorted {
		if i == 0 || !cmp.tied(sorted[i-1].Value, r.Value) {
			rank = i + 1
			dense++
		}
		ranked[i] = RankedRow{
			Row:       r,
			RowNumber: i + 1,
			Rank:      rank,
			DenseRank: dense,
		}
	}
	return ranked
}
