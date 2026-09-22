package ontology

import (
	"math"
	"sort"
)

// Rank partitions rows, ranks each partition, and returns all output rows
// ordered by partition key (lexicographic) then by ROW_NUMBER within a
// partition. The input slice and its elements are never modified.
func Rank(rows []Row, dir Direction) Result {
	groups := make(map[string][]Row)
	keys := make([]string, 0)
	var stats Stats

	for _, r := range rows {
		if r.Partition == nil {
			stats.SkippedNilPartition++
			continue
		}
		if math.IsNaN(r.SortValue) {
			stats.SkippedNaN++
			continue
		}
		key := *r.Partition
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		// Copy the struct: sorting and any later handling must never
		// touch the caller's Row values.
		groups[key] = append(groups[key], Row{
			Partition: r.Partition,
			SortValue: r.SortValue,
			ID:        r.ID,
		})
	}

	sort.Strings(keys)
	out := make([]RankedRow, 0, len(rows))
	for _, key := range keys {
		out = append(out, rankPartition(groups[key], dir, nil)...)
	}
	return Result{Rows: out, Stats: stats}
}

// rankPartition assigns ROW_NUMBER / RANK / DENSE_RANK to one partition's
// rows. rows is an internal working copy; callers never pass user memory in.
// counter may be nil; when supplied it records every value comparison.
func rankPartition(rows []Row, dir Direction, counter *comparisonCounter) []RankedRow {
	if counter == nil {
		counter = &comparisonCounter{}
	}
	sort.Sort(rowSorter{rows: rows, dir: dir, count: counter})

	out := make([]RankedRow, len(rows))
	rank, dense := 0, 0
	for i := range rows {
		if i == 0 || !sameValue(rows[i-1].SortValue, rows[i].SortValue) {
			rank = i + 1
			dense++
		}
		out[i] = RankedRow{
			Partition: *rows[i].Partition,
			SortValue: rows[i].SortValue,
			ID:        rows[i].ID,
			RowNumber: i + 1,
			Rank:      rank,
			DenseRank: dense,
		}
	}
	return out
}

// sameValue reports whether two accepted values tie. +0.0 and -0.0 tie.
func sameValue(a, b float64) bool {
	return !valueLess(a, b) && !valueLess(b, a)
}
