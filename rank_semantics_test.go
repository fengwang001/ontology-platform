package ontology

import (
	"math/rand"
	"testing"
)

func strptr(s string) *string { return &s }

func makeRows(values []float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		rows[i] = Row{Partition: strptr("p"), Value: v, ID: string(rune('a' + i))}
	}
	return rows
}

func expectColumns(t *testing.T, res RankResult, rn, rank, dense []int) {
	t.Helper()
	if len(res.Rows) != len(rn) {
		t.Fatalf("got %d rows, want %d", len(res.Rows), len(rn))
	}
	for i := range res.Rows {
		got := [3]int{res.Rows[i].RowNumber, res.Rows[i].Rank, res.Rows[i].DenseRank}
		want := [3]int{rn[i], rank[i], dense[i]}
		if got != want {
			t.Errorf("row %d (id=%s val=%v): (rn,rank,dense)=%v want %v",
				i, res.Rows[i].ID, res.Rows[i].Value, got, want)
		}
	}
}

func TestThreeColumns_10_20_20_30(t *testing.T) {
	res := Rank(makeRows([]float64{10, 20, 20, 30}), Ascending)
	expectColumns(t, res,
		[]int{1, 2, 3, 4}, // ROW_NUMBER
		[]int{1, 2, 2, 4}, // RANK: gap after tie
		[]int{1, 2, 2, 3}) // DENSE_RANK: no gap
}

func TestThreeColumns_AllTies(t *testing.T) {
	res := Rank(makeRows([]float64{5, 5, 5, 5}), Ascending)
	expectColumns(t, res,
		[]int{1, 2, 3, 4},
		[]int{1, 1, 1, 1},
		[]int{1, 1, 1, 1})
}

func TestThreeColumns_MiddleTie(t *testing.T) {
	// IDs a..e for values [1,2,2,2,3].
	res := Rank(makeRows([]float64{1, 2, 2, 2, 3}), Ascending)
	expectColumns(t, res,
		[]int{1, 2, 3, 4, 5},
		[]int{1, 2, 2, 2, 5},
		[]int{1, 2, 2, 2, 3})
}

func TestTieBreakByID_NotInputPosition(t *testing.T) {
	// Tie group with IDs deliberately out of input order.
	rows := []Row{
		{Partition: strptr("p"), Value: 20, ID: "c"},
		{Partition: strptr("p"), Value: 10, ID: "z"},
		{Partition: strptr("p"), Value: 20, ID: "a"},
		{Partition: strptr("p"), Value: 20, ID: "b"},
	}
	res := Rank(rows, Ascending)
	wantIDs := []string{"z", "a", "b", "c"}
	for i, id := range wantIDs {
		if res.Rows[i].ID != id {
			t.Fatalf("position %d id=%s want %s", i, res.Rows[i].ID, id)
		}
		if res.Rows[i].RowNumber != i+1 {
			t.Fatalf("row number at %d = %d", i, res.Rows[i].RowNumber)
		}
	}
}

func snapshot(res RankResult) []RankedRow {
	out := make([]RankedRow, len(res.Rows))
	copy(out, res.Rows)
	return out
}

func TestPermutationsStable(t *testing.T) {
	values := []float64{10, 20, 20, 30, 20, 10, 30, 5}
	base := makeRows(values)
	want := snapshot(Rank(base, Ascending))

	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 20; iter++ {
		shuffled := make([]Row, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := Rank(shuffled, Ascending)
		if len(got.Rows) != len(want) {
			t.Fatalf("iter %d: %d rows want %d", iter, len(got.Rows), len(want))
		}
		for i := range want {
			if got.Rows[i] != want[i] {
				t.Fatalf("iter %d row %d = %+v want %+v", iter, i, got.Rows[i], want[i])
			}
		}
	}
}

func TestDescendingIsNotReversed(t *testing.T) {
	rows := makeRows([]float64{10, 20, 20, 30}) // IDs a,b,c,d
	asc := Rank(rows, Ascending)
	desc := Rank(rows, Descending)

	// Values descend, ties keep ascending ID: b before c.
	wantIDs := []string{"d", "b", "c", "a"}
	for i, id := range wantIDs {
		if desc.Rows[i].ID != id {
			t.Fatalf("desc position %d id=%s want %s", i, desc.Rows[i].ID, id)
		}
	}
	expectColumns(t, desc,
		[]int{1, 2, 3, 4},
		[]int{1, 2, 2, 4},
		[]int{1, 2, 2, 3})

	// A plain reversal of ascending output would place c (later ID) before b.
	for i := range asc.Rows {
		if desc.Rows[i] == asc.Rows[len(asc.Rows)-1-i] {
			t.Fatalf("desc row %d is exact reversal of asc", i)
		}
	}
}
