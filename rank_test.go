package ontology

import (
	"fmt"
	"testing"
)

func ptr(s string) *string { return &s }

// rowsForValues builds one partition of rows with stable IDs "id1".."idN".
func rowsForValues(values ...float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		rows[i] = Row{Partition: ptr("p"), Value: v, ID: fmt.Sprintf("id%d", i+1)}
	}
	return rows
}

type expectedRank struct {
	id        string
	rowNumber int
	rank      int
	denseRank int
}

func assertRanks(t *testing.T, got []RankedRow, want []expectedRank) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Row.ID != w.id ||
			got[i].RowNumber != w.rowNumber ||
			got[i].Rank != w.rank ||
			got[i].DenseRank != w.denseRank {
			t.Errorf("row %d (%s): got (rn=%d rank=%d dense=%d), want (rn=%d rank=%d dense=%d)",
				i, got[i].Row.ID,
				got[i].RowNumber, got[i].Rank, got[i].DenseRank,
				w.rowNumber, w.rank, w.denseRank)
		}
	}
}

func TestRankColumns_10_20_20_30(t *testing.T) {
	// [10,20,20,30]: ROW_NUMBER 1,2,3,4; RANK 1,2,2,4; DENSE_RANK 1,2,2,3.
	res := Rank(rowsForValues(10, 20, 20, 30), Options{})
	assertRanks(t, res.Rows, []expectedRank{
		{"id1", 1, 1, 1},
		{"id2", 2, 2, 2},
		{"id3", 3, 2, 2},
		{"id4", 4, 4, 3},
	})
}

func TestRankColumns_AllTies(t *testing.T) {
	// [5,5,5,5]: ROW_NUMBER 1,2,3,4 while RANK and DENSE_RANK are all 1.
	res := Rank(rowsForValues(5, 5, 5, 5), Options{})
	assertRanks(t, res.Rows, []expectedRank{
		{"id1", 1, 1, 1},
		{"id2", 2, 1, 1},
		{"id3", 3, 1, 1},
		{"id4", 4, 1, 1},
	})
}

func TestRankColumns_1_2_2_2_3(t *testing.T) {
	// [1,2,2,2,3]: a triple tie in the middle.
	res := Rank(rowsForValues(1, 2, 2, 2, 3), Options{})
	assertRanks(t, res.Rows, []expectedRank{
		{"id1", 1, 1, 1},
		{"id2", 2, 2, 2},
		{"id3", 3, 2, 2},
		{"id4", 4, 2, 2},
		{"id5", 5, 5, 3},
	})
}

func TestRankColumns_DistinctValues(t *testing.T) {
	// With no ties all three columns coincide.
	res := Rank(rowsForValues(30, 10, 40, 20), Options{})
	assertRanks(t, res.Rows, []expectedRank{
		{"id2", 1, 1, 1},
		{"id4", 2, 2, 2},
		{"id1", 3, 3, 3},
		{"id3", 4, 4, 4},
	})
}
