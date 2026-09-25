package rank

import (
	"testing"
)

func strptr(s string) *string { return &s }

// makeRows builds rows in one fixed partition with the given sort values;
// IDs are derived from position but never depend on input position when
// ranking (tests may permute the slice freely).
func makeRows(values ...float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		id := string(rune('a' + i))
		rows[i] = Row{Partition: strptr("p"), ID: id, SortValue: v}
	}
	return rows
}

// comparisonBound is 10*n*ceil(log2(n+1)), the required O(n log n) cap.
func comparisonBound(n int) int64 {
	if n <= 1 {
		return int64(10 * n)
	}
	k := 0
	for (1 << k) < n+1 {
		k++
	}
	return int64(10*n) * int64(k)
}

func assertTriple(t *testing.T, got RankedRow, rn, rk, dr int) {
	t.Helper()
	if got.RowNumber != rn || got.Rank != rk || got.DenseRank != dr {
		t.Fatalf("id=%s value=%v: got (rn=%d r=%d dr=%d), want (%d,%d,%d)",
			got.ID, got.SortValue, got.RowNumber, got.Rank, got.DenseRank, rn, rk, dr)
	}
}
