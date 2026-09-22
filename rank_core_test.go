package ontology

import (
	"fmt"
	"testing"
)

func strptr(s string) *string { return &s }

// rowsFrom builds single-partition rows; IDs are "id<zero-padded index>"
// so that ID ascending order matches the given value order, which keeps
// expected sequences easy to read.
func rowsFrom(values []float64) []Row {
	rows := make([]Row, len(values))
	for i, v := range values {
		rows[i] = Row{Partition: strptr("p"), SortValue: v, ID: fmt.Sprintf("id%03d", i)}
	}
	return rows
}

type triple struct{ rn, rank, dense int }

func triplesOf(t *testing.T, res Result) []triple {
	t.Helper()
	got := make([]triple, len(res.Rows))
	for i, r := range res.Rows {
		got[i] = triple{r.RowNumber, r.Rank, r.DenseRank}
	}
	return got
}

func assertTriples(t *testing.T, name string, values []float64, want []triple) {
	t.Helper()
	res := Rank(rowsFrom(values), Asc)
	got := triplesOf(t, res)
	if len(got) != len(want) {
		t.Fatalf("%s: got %d rows, want %d", name, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s row %d: got (rn=%d rank=%d dense=%d), want %+v",
				name, i, got[i].rn, got[i].rank, got[i].dense, want[i])
		}
	}
}

func TestRank10_20_20_30(t *testing.T) {
	assertTriples(t, "[10,20,20,30]",
		[]float64{10, 20, 20, 30},
		[]triple{{1, 1, 1}, {2, 2, 2}, {3, 2, 2}, {4, 4, 3}})
}

func TestRankAllTies(t *testing.T) {
	assertTriples(t, "[5,5,5,5]",
		[]float64{5, 5, 5, 5},
		[]triple{{1, 1, 1}, {2, 1, 1}, {3, 1, 1}, {4, 1, 1}})
}

func TestRankMiddleTripleTie(t *testing.T) {
	assertTriples(t, "[1,2,2,2,3]",
		[]float64{1, 2, 2, 2, 3},
		[]triple{
			{1, 1, 1},
			{2, 2, 2}, {3, 2, 2}, {4, 2, 2},
			{5, 5, 3},
		})
}
