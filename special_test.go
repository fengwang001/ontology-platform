package ontology

import (
	"math"
	"testing"
)

// TestNaNRejected: NaN sort values are excluded from all ranking and
// counted in SkippedNaN.
func TestNaNRejected(t *testing.T) {
	rows := []Row{
		{Partition: StringPtr("p"), Value: math.NaN(), ID: "bad1"},
		{Partition: StringPtr("p"), Value: 1, ID: "ok1"},
		{Partition: StringPtr("p"), Value: math.NaN(), ID: "bad2"},
		{Partition: StringPtr("p"), Value: 2, ID: "ok2"},
	}
	sum := Compute(rows, Options{})
	if sum.SkippedNaN != 2 {
		t.Fatalf("SkippedNaN = %d, want 2", sum.SkippedNaN)
	}
	assertRanks(t, sum.Rows, []wantRank{{1, 1, 1}, {2, 2, 2}})
	for _, r := range sum.Rows {
		if math.IsNaN(r.Row.Value) {
			t.Fatalf("NaN row %s leaked into ranking", r.Row.ID)
		}
	}
}

// TestSignedZeroTie: +0.0 and -0.0 are equal and form a tie.
func TestSignedZeroTie(t *testing.T) {
	rows := []Row{
		{Partition: StringPtr("p"), Value: 0.0, ID: "a"},
		{Partition: StringPtr("p"), Value: math.Copysign(0, -1), ID: "b"},
		{Partition: StringPtr("p"), Value: 1, ID: "c"},
	}
	sum := Compute(rows, Options{})
	assertRanks(t, sum.Rows, []wantRank{
		{1, 1, 1},
		{2, 1, 1},
		{3, 3, 2},
	})
}

// TestInfinity: ±Inf are legal sort values and land at the extremes.
func TestInfinity(t *testing.T) {
	inf := math.Inf(1)
	rows := []Row{
		{Partition: StringPtr("p"), Value: inf, ID: "pos"},
		{Partition: StringPtr("p"), Value: 0, ID: "mid"},
		{Partition: StringPtr("p"), Value: math.Inf(-1), ID: "neg"},
		{Partition: StringPtr("p"), Value: inf, ID: "pos2"},
	}
	asc := Compute(rows, Options{})
	assertRanks(t, asc.Rows, []wantRank{
		{1, 1, 1}, // -Inf
		{2, 2, 2}, // 0
		{3, 3, 3}, // +Inf
		{4, 3, 3}, // +Inf
	})
	if asc.Rows[0].Row.ID != "neg" || asc.Rows[3].Row.ID != "pos2" {
		t.Fatalf("infinity rows misplaced: %v", asc.Rows)
	}

	desc := Compute(rows, Options{Descending: true})
	if desc.Rows[0].Row.Value != inf || desc.Rows[3].Row.Value != math.Inf(-1) {
		t.Fatalf("descending infinity order wrong: %v", desc.Rows)
	}
	assertRanks(t, desc.Rows, []wantRank{
		{1, 1, 1},
		{2, 1, 1},
		{3, 3, 2},
		{4, 4, 3},
	})
}
