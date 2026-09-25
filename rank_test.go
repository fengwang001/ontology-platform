package ontology

import (
	"math"
	"testing"
)

func strptr(s string) *string { return &s }

func makeRows(p string, vals []float64, ids []string) []Row {
	rows := make([]Row, len(vals))
	for i := range vals {
		rows[i] = Row{Partition: strptr(p), Value: vals[i], ID: ids[i]}
	}
	return rows
}

func seqIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = string(rune('a' + i))
	}
	return ids
}

func TestThreeRankSemantics(t *testing.T) {
	cases := []struct {
		name       string
		vals       []float64
		rowNumbers []int
		ranks      []int
		dense      []int
	}{
		{
			name:       "10,20,20,30",
			vals:       []float64{10, 20, 20, 30},
			rowNumbers: []int{1, 2, 3, 4},
			ranks:      []int{1, 2, 2, 4},
			dense:      []int{1, 2, 2, 3},
		},
		{
			name:       "5,5,5,5",
			vals:       []float64{5, 5, 5, 5},
			rowNumbers: []int{1, 2, 3, 4},
			ranks:      []int{1, 1, 1, 1},
			dense:      []int{1, 1, 1, 1},
		},
		{
			name:       "1,2,2,2,3",
			vals:       []float64{1, 2, 2, 2, 3},
			rowNumbers: []int{1, 2, 3, 4, 5},
			ranks:      []int{1, 2, 2, 2, 5},
			dense:      []int{1, 2, 2, 2, 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Rank(makeRows("p", tc.vals, seqIDs(len(tc.vals))), Asc)
			if len(res.Rankings) != len(tc.vals) {
				t.Fatalf("got %d rankings, want %d", len(res.Rankings), len(tc.vals))
			}
			for i, r := range res.Rankings {
				if r.RowNumber != tc.rowNumbers[i] || r.Rank != tc.ranks[i] || r.DenseRank != tc.dense[i] {
					t.Fatalf("row %d (%v): got rn=%d rank=%d dense=%d, want %d/%d/%d",
						i, r.ID, r.RowNumber, r.Rank, r.DenseRank,
						tc.rowNumbers[i], tc.ranks[i], tc.dense[i])
				}
			}
		})
	}
}

func TestSpecialFloatValues(t *testing.T) {
	p := "p"
	rows := []Row{
		{Partition: &p, Value: math.Inf(-1), ID: "neg-inf"},
		{Partition: &p, Value: 0, ID: "plus-zero"},
		{Partition: &p, Value: math.Copysign(0, -1), ID: "minus-zero"},
		{Partition: &p, Value: math.Inf(1), ID: "pos-inf"},
	}
	res := Rank(rows, Asc)
	wantOrder := []string{"neg-inf", "minus-zero", "plus-zero", "pos-inf"}
	for i, want := range wantOrder {
		if res.Rankings[i].ID != want {
			t.Fatalf("pos %d: got id %s, want %s", i, res.Rankings[i].ID, want)
		}
	}
	zeros := []int{res.Rankings[1].Rank, res.Rankings[1].DenseRank,
		res.Rankings[2].Rank, res.Rankings[2].DenseRank}
	if zeros[0] != 2 || zeros[1] != 2 || zeros[2] != 2 || zeros[3] != 2 {
		t.Fatalf("+0/-0 must tie at rank 2 dense 2: %v", zeros)
	}
	if res.Rankings[1].RowNumber != 2 || res.Rankings[2].RowNumber != 3 {
		t.Fatalf("zero tie row numbers wrong: %d %d",
			res.Rankings[1].RowNumber, res.Rankings[2].RowNumber)
	}
}
