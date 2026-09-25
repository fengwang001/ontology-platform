package ontology

import (
	"math"
	"testing"
)

func TestNaNRowsAreRejected(t *testing.T) {
	rows := []Row{
		{Partition: ptr("p"), Value: 1, ID: "ok1"},
		{Partition: ptr("p"), Value: math.NaN(), ID: "nan1"},
		{Partition: ptr("p"), Value: 2, ID: "ok2"},
		{Partition: ptr("p"), Value: math.NaN(), ID: "nan2"},
	}
	res := Rank(rows, Options{})

	if res.SkippedNaN != 2 || res.Skipped() != 2 {
		t.Fatalf("skip counts wrong: NaN=%d total=%d", res.SkippedNaN, res.Skipped())
	}
	if len(res.Rows) != 2 {
		t.Fatalf("NaN rows participated in ranking: %v", res.Rows)
	}
	assertRanks(t, res.Rows, []expectedRank{
		{"ok1", 1, 1, 1},
		{"ok2", 2, 2, 2},
	})
}

func TestSignedZeroTies(t *testing.T) {
	// +0.0 and -0.0 must compare equal and therefore form a tie.
	res := Rank([]Row{
		{Partition: ptr("p"), Value: 0, ID: "plus"},
		{Partition: ptr("p"), Value: math.Copysign(0, -1), ID: "minus"},
	}, Options{})
	assertRanks(t, res.Rows, []expectedRank{
		{"minus", 1, 1, 1},
		{"plus", 2, 1, 1},
	})
}

func TestInfinitiesRankAtTheEnds(t *testing.T) {
	res := Rank([]Row{
		{Partition: ptr("p"), Value: math.Inf(1), ID: "pos"},
		{Partition: ptr("p"), Value: 0, ID: "zero"},
		{Partition: ptr("p"), Value: math.Inf(-1), ID: "neg"},
	}, Options{})
	assertRanks(t, res.Rows, []expectedRank{
		{"neg", 1, 1, 1},
		{"zero", 2, 2, 2},
		{"pos", 3, 3, 3},
	})

	desc := Rank([]Row{
		{Partition: ptr("p"), Value: math.Inf(1), ID: "pos"},
		{Partition: ptr("p"), Value: 0, ID: "zero"},
		{Partition: ptr("p"), Value: math.Inf(-1), ID: "neg"},
	}, Options{Descending: true})
	assertRanks(t, desc.Rows, []expectedRank{
		{"pos", 1, 1, 1},
		{"zero", 2, 2, 2},
		{"neg", 3, 3, 3},
	})
}
