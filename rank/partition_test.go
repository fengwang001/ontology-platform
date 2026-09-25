package rank

import (
	"math"
	"testing"
)

// TestPartitionIsolation checks that each partition ranks from 1
// independently and that partitions appear in key lexicographic
// order, with the empty string as a valid (first) partition.
func TestPartitionIsolation(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("b"), Value: 1, ID: 1},
		{Partition: strPtr("b"), Value: 1, ID: 2},
		{Partition: strPtr("a"), Value: 9, ID: 3},
		{Partition: strPtr(""), Value: 5, ID: 4},
		{Partition: strPtr(""), Value: 6, ID: 5},
		{Partition: strPtr("a"), Value: 9, ID: 6},
	}
	res := Compute(rows, Options{})
	if len(res.Rows) != 6 {
		t.Fatalf("got %d rows, want 6", len(res.Rows))
	}

	var gotParts []string
	for _, r := range res.Rows {
		gotParts = append(gotParts, *r.Row.Partition)
	}
	wantParts := []string{"", "", "a", "a", "b", "b"}
	for i := range wantParts {
		if gotParts[i] != wantParts[i] {
			t.Fatalf("partition order: got %q, want %q", gotParts, wantParts)
		}
	}

	assertTriples(t, res, [][3]int{
		{1, 1, 1}, // "" value 5
		{2, 2, 2}, // "" value 6
		{1, 1, 1}, // "a" value 9
		{2, 1, 1}, // "a" value 9
		{1, 1, 1}, // "b" value 1
		{2, 1, 1}, // "b" value 1
	})
}

// TestSkippedRows checks that nil partition keys and NaN values are
// rejected, counted separately, and excluded from ranking.
func TestSkippedRows(t *testing.T) {
	rows := []Row{
		{Partition: nil, Value: 1, ID: 1},
		{Partition: nil, Value: 2, ID: 2},
		{Partition: strPtr("p"), Value: math.NaN(), ID: 3},
		{Partition: strPtr("p"), Value: 10, ID: 4},
		{Partition: strPtr("p"), Value: 20, ID: 5},
	}
	res := Compute(rows, Options{})
	if res.SkippedNilPartition != 2 {
		t.Errorf("SkippedNilPartition = %d, want 2", res.SkippedNilPartition)
	}
	if res.SkippedNaN != 1 {
		t.Errorf("SkippedNaN = %d, want 1", res.SkippedNaN)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("got %d ranked rows, want 2", len(res.Rows))
	}
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
	})
}

// TestSignedZeroTie: +0.0 and -0.0 must compare equal and tie.
func TestSignedZeroTie(t *testing.T) {
	rows := []Row{
		{Partition: strPtr("p"), Value: 0.0, ID: 1},
		{Partition: strPtr("p"), Value: math.Copysign(0, -1), ID: 2},
		{Partition: strPtr("p"), Value: 1, ID: 3},
	}
	res := Compute(rows, Options{})
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 1, 1},
		{3, 3, 2},
	})
}

// TestInfinities: ±Inf are legal sort values and land at the ends.
func TestInfinities(t *testing.T) {
	inf := math.Inf(1)
	rows := []Row{
		{Partition: strPtr("p"), Value: inf, ID: 1},
		{Partition: strPtr("p"), Value: 0, ID: 2},
		{Partition: strPtr("p"), Value: -inf, ID: 3},
		{Partition: strPtr("p"), Value: inf, ID: 4},
	}
	res := Compute(rows, Options{})
	wantIDs := []int64{3, 2, 1, 4}
	if got := ids(res); !equalIDs(got, wantIDs) {
		t.Fatalf("asc with infinities: got ids %v, want %v", got, wantIDs)
	}
	assertTriples(t, res, [][3]int{
		{1, 1, 1},
		{2, 2, 2},
		{3, 3, 3},
		{4, 3, 3},
	})

	desc := Compute(rows, Options{Desc: true})
	wantDesc := []int64{1, 4, 2, 3}
	if got := ids(desc); !equalIDs(got, wantDesc) {
		t.Fatalf("desc with infinities: got ids %v, want %v", got, wantDesc)
	}
}
