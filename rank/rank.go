package rank

import "sort"

// Rank computes ROW_NUMBER, RANK and DENSE_RANK for every accepted row,
// independently per partition.
//
// Rows are rejected (and counted in the returned Result) when Partition is
// nil or when SortValue is NaN; rejected rows take no part in ranking.
// The input slice and the Row values it contains are never modified: all
// sorting happens on copies. The result slice is freshly allocated and is
// ordered by partition key lexicographically, then within a partition by
// the requested direction (ties broken by ID ascending).
func Rank(rows []Row, dir Direction) Result {
	var res Result

	// Group copies of accepted rows per partition. Copying means later
	// sorting cannot reorder or mutate the caller's slice or its rows.
	groups := make(map[string][]Row)
	for _, r := range rows {
		if r.Partition == nil {
			res.SkippedNilPartition++
			continue
		}
		if isNaN(r.SortValue) {
			res.SkippedNaN++
			continue
		}
		key := *r.Partition
		groups[key] = append(groups[key], r)
	}

	// Deterministic partition order: sorted keys, never map iteration.
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		group := groups[key]
		var comparisons int64
		sort.Slice(group, comparator(group, dir, &comparisons))
		res.Comparisons += comparisons

		ranked := make([]RankedRow, len(group))
		for i, r := range group {
			ranked[i] = RankedRow{
				Partition: key,
				ID:        r.ID,
				SortValue: r.SortValue,
			}
		}
		assignRanks(group, ranked)
		res.Rows = append(res.Rows, ranked...)
	}

	if res.Rows == nil {
		res.Rows = []RankedRow{}
	}
	return res
}
