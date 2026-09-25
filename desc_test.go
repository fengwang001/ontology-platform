package ontology

import (
	"reflect"
	"testing"
)

// descRows values: 1(b) 2(d) 2(a) 3(c). IDs inside the tie are deliberately
// out of order so that reversing the ascending output would put them d,a
// instead of the required a,d.
func descRows() []Row {
	return []Row{
		{Partition: ptr("p"), Value: 1, ID: "b"},
		{Partition: ptr("p"), Value: 2, ID: "d"},
		{Partition: ptr("p"), Value: 2, ID: "a"},
		{Partition: ptr("p"), Value: 3, ID: "c"},
	}
}

func reverseRanked(in []RankedRow) []RankedRow {
	out := make([]RankedRow, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}

func TestDescendingIsNotReversedAscending(t *testing.T) {
	asc := Rank(descRows(), Options{Descending: false})
	desc := Rank(descRows(), Options{Descending: true})

	assertRanks(t, asc.Rows, []expectedRank{
		{"b", 1, 1, 1}, // value 1
		{"a", 2, 2, 2}, // value 2, tie IDs ascending
		{"d", 3, 2, 2},
		{"c", 4, 4, 3}, // value 3
	})

	assertRanks(t, desc.Rows, []expectedRank{
		{"c", 1, 1, 1}, // value 3
		{"a", 2, 2, 2}, // value 2, IDs still ascending inside the tie
		{"d", 3, 2, 2},
		{"b", 4, 4, 3}, // value 1
	})

	if reflect.DeepEqual(desc.Rows, reverseRanked(asc.Rows)) {
		t.Fatalf("descending output equals reversed ascending output: %v", desc.Rows)
	}
}

func TestDescendingTieRulesUnchanged(t *testing.T) {
	// Values [1,2,2,3] descending produce 3,2,2,1: ranks keep the same
	// gap rule (1,2,2,4) and dense rule (1,2,2,3), only order changes.
	res := Rank(rowsForValues(1, 2, 2, 3), Options{Descending: true})
	assertRanks(t, res.Rows, []expectedRank{
		{"id4", 1, 1, 1},
		{"id2", 2, 2, 2},
		{"id3", 3, 2, 2},
		{"id1", 4, 4, 3},
	})
}
