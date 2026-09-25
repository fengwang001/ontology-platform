package rank

// assignRanks fills RowNumber/Rank/DenseRank for one already-sorted
// partition. sorted is ordered by sort value (per direction) with IDs
// ascending inside ties.
//
// On the sequence 10,20,20,30 this yields:
//
//	ROW_NUMBER 1,2,3,4
//	RANK       1,2,2,4 (ties share a rank, then the rank skips)
//	DENSE_RANK 1,2,2,3 (ties share a rank, no numbers are skipped)
func assignRanks(sorted []Row, out []RankedRow) {
	dense := 0
	for i := range sorted {
		out[i].RowNumber = i + 1
		if i == 0 || !valueEqual(sorted[i-1].SortValue, sorted[i].SortValue) {
			dense++
		}
		// RANK is the row number of the first row in the tie group.
		out[i].Rank = i + 1
		if i > 0 && valueEqual(sorted[i-1].SortValue, sorted[i].SortValue) {
			out[i].Rank = out[i-1].Rank
		}
		out[i].DenseRank = dense
	}
}
