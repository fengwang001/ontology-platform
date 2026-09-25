package rank

import (
	"math"
	"sort"
)

// Rank computes ROW_NUMBER, RANK, and DENSE_RANK for every accepted
// row, independently within each partition. Rows with a nil Partition
// or a NaN Value are rejected and counted in the Result skip counters.
//
// The input slice and the Row values it holds are never modified; the
// returned Result.Rows slice is newly allocated.
func Rank(rows []Row, cfg Config) Result {
	cmp := cfg.Compare
	if cmp == nil {
		cmp = compareValues
	}

	var res Result
	groups := make(map[string][]Row)
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
		groups[key] = append(groups[key], r)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	res.Rows = make([]RankedRow, 0, len(rows)-res.Skipped)
	for _, k := range keys {
		res.Rows = append(res.Rows, rankPartition(groups[k], cfg, cmp)...)
	}
	return res
}

// rankPartition sorts one partition's rows and assigns the three
// ranking columns. It sorts a copy and never touches the caller's data.
func rankPartition(group []Row, cfg Config, cmp func(a, b Row) int) []RankedRow {
	sorted := make([]Row, len(group))
	copy(sorted, group)
	sort.Slice(sorted, func(i, j int) bool {
		return fullCompare(cfg, cmp, sorted[i], sorted[j]) < 0
	})

	out := make([]RankedRow, len(sorted))
	rank, dense := 0, 0
	for i, r := range sorted {
		if i == 0 || cmp(sorted[i-1], r) != 0 {
			rank = i + 1
			dense++
		}
		out[i] = RankedRow{
			Row:       r,
			RowNumber: i + 1,
			Rank:      rank,
			DenseRank: dense,
		}
	}
	return out
}
