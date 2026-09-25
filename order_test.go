package ontology

import "testing"

func TestDescendingNotReversedAscending(t *testing.T) {
	rows := mkRows([]float64{10, 20, 20, 30}, "r")
	asc := Rank(rows, Asc).Rows
	desc := Rank(rows, Desc).Rows

	wantDesc := []struct {
		id                string
		row, rank, dense  int
	}{
		{"r4", 1, 1, 1},
		{"r2", 2, 2, 2},
		{"r3", 3, 2, 2},
		{"r1", 4, 4, 3},
	}
	for i, w := range wantDesc {
		if desc[i].ID != w.id || desc[i].RowNumber != w.row ||
			desc[i].Rank != w.rank || desc[i].DenseRank != w.dense {
			t.Fatalf("desc row %d = (%s,%d,%d,%d), want (%s,%d,%d,%d)",
				i, desc[i].ID, desc[i].RowNumber, desc[i].Rank, desc[i].DenseRank,
				w.id, w.row, w.rank, w.dense)
		}
	}

	// A plain reversal of ascending output would order the tied IDs r3,r2.
	// Descending must keep ties in ascending ID order r2,r3, so the two
	// orderings are not reversals of each other.
	reversedIDs := []string{asc[3].ID, asc[2].ID, asc[1].ID, asc[0].ID}
	descIDs := []string{desc[0].ID, desc[1].ID, desc[2].ID, desc[3].ID}
	if equalStrings(descIDs, reversedIDs) {
		t.Fatalf("descending %v must not be the reversal of ascending %v",
			descIDs, reversedIDs)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
