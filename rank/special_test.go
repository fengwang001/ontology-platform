package rank

import (
	"math"
	"testing"
)

// NaN sort values are rejected and counted; they never take part in
// ranking.
func TestNaNSkipped(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: math.NaN(), ID: "nan1"},
		{Partition: strptr("p"), Value: 1, ID: "ok1"},
		{Partition: strptr("p"), Value: math.NaN(), ID: "nan2"},
		{Partition: strptr("p"), Value: 2, ID: "ok2"},
	}
	res := Rank(rows, Config{})
	if res.Skipped != 2 || res.SkippedNaN != 2 {
		t.Fatalf("Skipped=%d NaN=%d, want 2 and 2", res.Skipped, res.SkippedNaN)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("len(Rows) = %d, want 2", len(res.Rows))
	}
	for _, rr := range res.Rows {
		if math.IsNaN(rr.Row.Value) {
			t.Fatalf("NaN row %q leaked into results", rr.Row.ID)
		}
	}
}

// +0.0 and -0.0 are equal sort keys and form a tie.
func TestSignedZeroTies(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: 0.0, ID: "a"},
		{Partition: strptr("p"), Value: math.Copysign(0, -1), ID: "b"},
		{Partition: strptr("p"), Value: 1, ID: "c"},
	}
	res := Rank(rows, Config{})
	assertColumns(t, res, []want3{
		{1, 1, 1},
		{2, 1, 1},
		{3, 3, 2},
	})
}

// +-Inf are legal sort values and land at the two ends.
func TestInfinitiesRanked(t *testing.T) {
	rows := []Row{
		{Partition: strptr("p"), Value: math.Inf(1), ID: "pos"},
		{Partition: strptr("p"), Value: 0, ID: "mid"},
		{Partition: strptr("p"), Value: math.Inf(-1), ID: "neg"},
	}
	asc := Rank(rows, Config{})
	wantIDs := []string{"neg", "mid", "pos"}
	for i, id := range wantIDs {
		if asc.Rows[i].Row.ID != id || asc.Rows[i].Rank != i+1 {
			t.Fatalf("asc row %d: id=%q rank=%d, want id=%q rank=%d",
				i, asc.Rows[i].Row.ID, asc.Rows[i].Rank, id, i+1)
		}
	}

	desc := Rank(rows, Config{Descending: true})
	wantDescIDs := []string{"pos", "mid", "neg"}
	for i, id := range wantDescIDs {
		if desc.Rows[i].Row.ID != id || desc.Rows[i].Rank != i+1 {
			t.Fatalf("desc row %d: id=%q rank=%d, want id=%q rank=%d",
				i, desc.Rows[i].Row.ID, desc.Rows[i].Rank, id, i+1)
		}
	}
}

// Mixed rejections accumulate in both the total and per-reason counts.
func TestSkipCountersAccumulate(t *testing.T) {
	rows := []Row{
		{Partition: nil, Value: 1, ID: "x"},
		{Partition: strptr("p"), Value: math.NaN(), ID: "y"},
		{Partition: nil, Value: math.NaN(), ID: "z"}, // nil checked first
		{Partition: strptr("p"), Value: 7, ID: "ok"},
	}
	res := Rank(rows, Config{})
	if res.Skipped != 3 {
		t.Fatalf("Skipped = %d, want 3", res.Skipped)
	}
	if res.SkippedNilPartition != 2 || res.SkippedNaN != 1 {
		t.Fatalf("NilPartition=%d NaN=%d, want 2 and 1",
			res.SkippedNilPartition, res.SkippedNaN)
	}
	if len(res.Rows) != 1 || res.Rows[0].Row.ID != "ok" {
		t.Fatalf("Rows = %v, want only ok", res.Rows)
	}
}
