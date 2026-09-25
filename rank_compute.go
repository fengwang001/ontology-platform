package ontology

// assignRanks turns an order-sorted partition into ranked rows.
//
// Equal values (including +0.0/-0.0) share a rank. Rank keeps the
// ordinal position and therefore leaves gaps after ties; denseRank counts
// only distinct values and never gaps. RowNumber is just the 1-based
// position, which is deterministic because ties were ordered by ID.
func assignRanks(partition string, items []rankedItem) []RankedRow {
	rows := make([]RankedRow, len(items))
	rank := 0
	denseRank := 0
	for i := range items {
		if i == 0 || compareValue(items[i-1].value, items[i].value) != 0 {
			rank = i + 1
			denseRank++
		}
		rows[i] = RankedRow{
			Partition: partition,
			Value:     items[i].value,
			ID:        items[i].id,
			RowNumber: i + 1,
			Rank:      rank,
			DenseRank: denseRank,
		}
	}
	return rows
}
