package ontology

import (
	"math"
	"sort"
)

// Rank computes ROW_NUMBER / RANK / DENSE_RANK for every accepted row.
//
// Rows with a nil Partition or a NaN Value are rejected and counted in the
// returned Result; they never participate in any ranking. Each remaining
// partition is ranked independently starting at 1. Partitions are emitted
// in lexicographic order of their keys (the empty string is a valid key).
//
// Within a partition rows are ordered by Value through Options.Compare
// (ascending by default, descending when Options.Descending is set); rows
// with equal values keep the ascending order of their ID regardless of
// direction. Neither the input slice nor the Row values are modified, and
// Result.Rows is a freshly allocated slice.
func Rank(rows []Row, opts Options) Result {
	cmp := opts.Compare
	if cmp == nil {
		cmp = CompareFloat
	}

	groups := make(map[string][]Row)
	var result Result

	// Copy each accepted Row by value into a per-partition slice, so all
	// later sorting happens on copies and the caller's slice is untouched.
	for _, row := range rows {
		if row.Partition == nil {
			result.SkippedNilPartition++
			continue
		}
		if math.IsNaN(row.Value) {
			result.SkippedNaN++
			continue
		}
		key := *row.Partition
		groups[key] = append(groups[key], row)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool {
			c := cmp(group[i].Value, group[j].Value)
			if c != 0 {
				if opts.Descending {
					return c > 0
				}
				return c < 0
			}
			// Ties are always broken by ascending ID, even descending.
			return group[i].ID < group[j].ID
		})

		rank := 0
		denseRank := 0
		for i := range group {
			if i == 0 || cmp(group[i-1].Value, group[i].Value) != 0 {
				rank = i + 1
				denseRank++
			}
			result.Rows = append(result.Rows, RankedRow{
				Row:       group[i],
				RowNumber: i + 1,
				Rank:      rank,
				DenseRank: denseRank,
			})
		}
	}

	return result
}
