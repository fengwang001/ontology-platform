package ontology

import "testing"

// makeRows builds rows in a single partition "p" with IDs r1..rn in
// the same order as the given values.
func makeRows(values ...float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		rows[i] = Row{
			Partition: StringPtr("p"),
			Value:     v,
			ID:        "r" + string(rune('1'+i)),
		}
	}
	return rows
}

type wantRank struct {
	rowNumber int
	rank      int
	dense     int
}

// assertRanks checks all three ranking columns row by row, in output
// order, against the expected table.
func assertRanks(t *testing.T, got []RankedRow, want []wantRank) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.RowNumber != w.rowNumber || g.Rank != w.rank || g.DenseRank != w.dense {
			t.Errorf("row %d (id=%s value=%v): got (rn=%d rank=%d dense=%d), want (rn=%d rank=%d dense=%d)",
				i, g.Row.ID, g.Row.Value,
				g.RowNumber, g.Rank, g.DenseRank,
				w.rowNumber, w.rank, w.dense)
		}
	}
}

func TestSemantics_10_20_20_30(t *testing.T) {
	sum := Compute(makeRows(10, 20, 20, 30), Options{})
	// ROW_NUMBER 1,2,3,4; RANK 1,2,2,4; DENSE_RANK 1,2,2,3.
	assertRanks(t, sum.Rows, []wantRank{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
}

func TestSemantics_AllTied(t *testing.T) {
	sum := Compute(makeRows(5, 5, 5, 5), Options{})
	// ROW_NUMBER 1,2,3,4; RANK all 1; DENSE_RANK all 1.
	assertRanks(t, sum.Rows, []wantRank{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
		{4, 1, 1},
	})
}

func TestSemantics_MiddleTies(t *testing.T) {
	sum := Compute(makeRows(1, 2, 2, 2, 3), Options{})
	// RANK 1,2,2,2,5; DENSE_RANK 1,2,2,2,3.
	assertRanks(t, sum.Rows, []wantRank{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	})
}
