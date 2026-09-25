package rank

import (
	"math"
	"sort"
)

// Compute calculates ROW_NUMBER, RANK and DENSE_RANK for every
// accepted row, independently per partition.
//
// Semantics, per partition, rows ordered by Value (ascending unless
// opts.Desc), ties broken by ID ascending:
//
//	RowNumber: position in the ordering, 1-based; unique per row.
//	Rank:      1 + number of rows strictly before this row's value;
//	           ties share a rank and leave gaps after them.
//	DenseRank: 1 + number of distinct values strictly before this
//	           row's value; ties share a rank with no gaps.
//
// Rows with a nil Partition or a NaN Value are rejected and counted
// in the Result. The input slice and its rows are never modified;
// the returned slice is freshly allocated.
func Compute(rows []Row, opts Options) Result {
	groups := make(map[string][]Row)
	var res Result
	for _, r := range rows {
		if r.Partition == nil {
			res.SkippedNilPartition++
			continue
		}
		if math.IsNaN(r.Value) {
			res.SkippedNaN++
			continue
		}
		groups[*r.Partition] = append(groups[*r.Partition], r)
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	total := 0
	for _, k := range keys {
		total += len(groups[k])
	}
	res.Rows = make([]RankedRow, 0, total)

	for _, k := range keys {
		res.Rows = append(res.Rows, rankPartition(groups[k], opts)...)
	}
	return res
}

// less reports whether row a sorts before row b under opts.
// Value order follows opts.Desc; ties always break by ID ascending,
// so the result never depends on input positions or map order.
func less(a, b Row, opts Options) bool {
	if opts.OnCompare != nil {
		opts.OnCompare()
	}
	if a.Value != b.Value {
		if opts.Desc {
			return a.Value > b.Value
		}
		return a.Value < b.Value
	}
	return a.ID < b.ID
}

// rankPartition sorts one partition's rows (a slice we own, built
// inside Compute) and assigns the three ranking values.
func rankPartition(rows []Row, opts Options) []RankedRow {
	sort.SliceStable(rows, func(i, j int) bool {
		return less(rows[i], rows[j], opts)
	})

	ranked := make([]RankedRow, len(rows))
	for i, r := range rows {
		ranked[i].Row = r
		ranked[i].RowNumber = i + 1
		if i == 0 || rows[i].Value != rows[i-1].Value {
			ranked[i].Rank = i + 1
			if i == 0 {
				ranked[i].DenseRank = 1
			} else {
				ranked[i].DenseRank = ranked[i-1].DenseRank + 1
			}
		} else {
			ranked[i].Rank = ranked[i-1].Rank
			ranked[i].DenseRank = ranked[i-1].DenseRank
		}
	}
	return ranked
}
