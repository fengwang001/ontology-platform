package rank_test

import (
	"testing"

	"ontology/rank"
)

// Descending order keeps the same tie semantics: RANK skips,
// DENSE_RANK does not, and ties are still ordered by row ID
// ascending (never by ID descending, never by input position).
func TestDescendingTieSemantics(t *testing.T) {
	rows := makeRows("p",
		[]string{"a", "b", "c", "d"},
		[]float64{10, 20, 20, 30})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{Descending: true}))

	// Sorted 30,20,20,10: same rank/dense-rank pattern as ascending.
	assertTriples(t, p, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 2, 2},
		{4, 4, 3},
	})
	// The tied 20s appear as b then c: ID ascending, not descending.
	if got := ids(p); !equalStrings(got, []string{"d", "b", "c", "a"}) {
		t.Errorf("id order = %v, want [d b c a]", got)
	}
}

// Descending output must not be the ascending output reversed: a
// reversal would order tied IDs descending and hand out row numbers
// in reverse; the real implementation does neither.
func TestDescendingIsNotReversedAscending(t *testing.T) {
	rows := makeRows("p",
		[]string{"a", "b", "c", "d"},
		[]float64{1, 2, 2, 3})

	asc := onlyPartition(t, rank.Compute(rows, rank.Options{}))
	desc := onlyPartition(t, rank.Compute(rows, rank.Options{Descending: true}))

	// What a naive "reverse the ascending result" would produce.
	reversedIDs := make([]string, len(asc.Rows))
	for i, r := range asc.Rows {
		reversedIDs[len(asc.Rows)-1-i] = r.Row.ID
	}
	gotIDs := ids(desc)
	if equalStrings(gotIDs, reversedIDs) {
		t.Fatalf("descending output %v equals reversed ascending %v", gotIDs, reversedIDs)
	}

	// And the real thing, explicitly: value desc, tie IDs asc.
	if want := []string{"d", "b", "c", "a"}; !equalStrings(gotIDs, want) {
		t.Errorf("descending id order = %v, want %v", gotIDs, want)
	}
	// ROW_NUMBER still runs 1..n along the descending order.
	for i, r := range desc.Rows {
		if r.RowNumber != i+1 {
			t.Errorf("row %d: RowNumber = %d, want %d", i, r.RowNumber, i+1)
		}
	}
}

// All-tied descending: ranks stay 1, IDs still ascending.
func TestDescendingAllTied(t *testing.T) {
	rows := makeRows("p",
		[]string{"w", "x", "y", "z"},
		[]float64{5, 5, 5, 5})
	p := onlyPartition(t, rank.Compute(rows, rank.Options{Descending: true}))
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
