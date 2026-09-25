package ontology

import "sort"

// assignRanks fills ROW_NUMBER/RANK/DENSE_RANK onto rows sorted by (value,id).
func assignRanks(rows []indexedRow) []RankRow {
	out := make([]RankRow, len(rows))
	dense, tieStart := 0, 0
	for i := range rows {
		if i == 0 || !equalValues(rows[i].value, rows[i-1].value) {
			tieStart = i
			dense++
		}
		out[i] = RankRow{
			ID:        rows[i].id,
			Partition: rows[i].partition,
			Value:     rows[i].value,
			RowNumber: i + 1,
			Rank:      tieStart + 1,
			DenseRank: dense,
		}
	}
	return out
}

// Rank computes the three rankings for all accepted rows. Invalid rows
// (nil partition key or NaN value) are skipped and counted. Output partitions
// are ordered lexicographically by key and the returned slice is freshly
// allocated; the input slice and its Row values are never modified.
func Rank(rows []Row, order Order) Report {
	groups := make(map[string][]indexedRow)
	skipped := 0
	for _, r := range rows {
		if invalidRow(r) {
			skipped++
			continue
		}
		key := *r.Partition
		groups[key] = append(groups[key], indexedRow{
			partition: key,
			value:     r.Value,
			id:        r.ID,
		})
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	cmp := &comparisonCounter{}
	out := make([]RankRow, 0, len(rows)-skipped)
	for _, key := range keys {
		group := groups[key]
		sortPartition(group, order, cmp)
		out = append(out, assignRanks(group)...)
	}
	return Report{Rows: out, Skipped: skipped, Comparisons: cmp.count}
}
