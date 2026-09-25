package rank_test

import (
	"testing"

	"ontology/rank"
)

// assertTriples checks the three ranking columns row by row.
func assertTriples(t *testing.T, p rank.PartitionResult, want [][3]int) {
	t.Helper()
	got := triples(p)
	if len(got) != len(want) {
		t.Fatalf("row count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d (id %s): got (row_number,rank,dense_rank)=%v, want %v",
				i, p.Rows[i].Row.ID, got[i], want[i])
		}
	}
}

// The canonical tie case: values [10,20,20,30] ascending must yield
// ROW_NUMBER 1,2,3,4 / RANK 1,2,2,4 / DENSE_RANK 1,2,2,3, with all
// three columns returned by the same call.
func TestSemanticsTieMiddle(t *testing.T) {
	rows := makeRows("p", []string{"a", "b", "c", "d"}, []float64{10, 20, 20, 30})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{}))
	assertTriples(t, p, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
	// Tied rows ordered by ID ascending.
	if got := ids(p); !equalStrings(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("id order = %v, want [a b c d]", got)
	}
}

// All rows tied: RANK and DENSE_RANK stay 1 everywhere while
// ROW_NUMBER still walks 1..n in ID order.
func TestSemanticsAllTied(t *testing.T) {
	rows := makeRows("p", []string{"w", "x", "y", "z"}, []float64{5, 5, 5, 5})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{}))
	assertTriples(t, p, [][3]int{
		{1, 1, 1},
		{2, 1, 1},
		{3, 1, 1},
		{4, 1, 1},
	})
	if got := ids(p); !equalStrings(got, []string{"w", "x", "y", "z"}) {
		t.Errorf("id order = %v, want [w x y z]", got)
	}
}

// Multiple ties in the middle: [1,2,2,2,3].
func TestSemanticsMultiTie(t *testing.T) {
	rows := makeRows("p",
		[]string{"a", "b", "c", "d", "e"},
		[]float64{1, 2, 2, 2, 3})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{}))
	assertTriples(t, p, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 2, 2},
		{5, 5, 3},
	})
}

// No ties at all: all three functions coincide.
func TestSemanticsNoTies(t *testing.T) {
	rows := makeRows("p",
		[]string{"a", "b", "c"},
		[]float64{3, 1, 2})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{}))
	assertTriples(t, p, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 3, 3},
	})
	if got := ids(p); !equalStrings(got, []string{"b", "c", "a"}) {
		t.Errorf("id order = %v, want [b c a]", got)
	}
}
