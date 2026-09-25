package rank_test

import (
	"testing"

	"ontology/rank"
)

// Each partition ranks independently from 1, partitions come out in
// lexicographic key order, and the empty string is a legal key.
func TestPartitionIsolationAndOrder(t *testing.T) {
	var rows []rank.Row
	rows = append(rows, makeRows("beta",
		[]string{"b1", "b2", "b3"}, []float64{9, 9, 1})...)
	rows = append(rows, makeRows("",
		[]string{"e1", "e2"}, []float64{2, 3})...)
	rows = append(rows, makeRows("alpha",
		[]string{"a1", "a2"}, []float64{5, 4})...)

	res := rank.Compute(rows, rank.Options{})
	if res.Skipped != 0 {
		t.Fatalf("skipped = %d, want 0", res.Skipped)
	}
	if len(res.Partitions) != 3 {
		t.Fatalf("partitions = %d, want 3", len(res.Partitions))
	}

	// Lexicographic order: "" < "alpha" < "beta".
	wantKeys := []string{"", "alpha", "beta"}
	for i, want := range wantKeys {
		if res.Partitions[i].Key != want {
			t.Fatalf("partition %d key = %q, want %q", i, res.Partitions[i].Key, want)
		}
	}

	// Each partition restarts all three rankings at 1.
	assertTriples(t, res.Partitions[0], [][3]int{{1, 1, 1}, {2, 2, 2}})
	assertTriples(t, res.Partitions[1], [][3]int{{1, 1, 1}, {2, 2, 2}})
	assertTriples(t, res.Partitions[2], [][3]int{{1, 1, 1}, {2, 2, 2}, {3, 2, 2}})
}

// Rows with a nil partition key are rejected and counted; they do not
// form a partition and do not affect anyone else's ranks.
func TestNilPartitionSkipped(t *testing.T) {
	rows := makeRows("p", []string{"a", "b"}, []float64{1, 2})
	rows = append(rows,
		rank.Row{Partition: nil, Value: 7, ID: "lost1"},
		rank.Row{Partition: nil, Value: 8, ID: "lost2"},
	)

	res := rank.Compute(rows, rank.Options{})
	if res.Skipped != 2 || res.SkippedNilPartition != 2 {
		t.Fatalf("skipped = %d (nil %d), want 2 (2)",
			res.Skipped, res.SkippedNilPartition)
	}
	if res.SkippedNaN != 0 {
		t.Fatalf("skipped NaN = %d, want 0", res.SkippedNaN)
	}
	if len(res.Partitions) != 1 {
		t.Fatalf("partitions = %d, want 1", len(res.Partitions))
	}
	assertTriples(t, res.Partitions[0], [][3]int{{1, 1, 1}, {2, 2, 2}})
}

// The empty-string partition is legal and distinct from a missing key.
func TestEmptyPartitionKeyIsLegal(t *testing.T) {
	rows := makeRows("", []string{"x", "y"}, []float64{2, 1})
	res := rank.Compute(rows, rank.Options{})
	if res.Skipped != 0 {
		t.Fatalf("skipped = %d, want 0", res.Skipped)
	}
	p := onlyPartition(t, res)
	if p.Key != "" {
		t.Fatalf("partition key = %q, want empty", p.Key)
	}
	assertTriples(t, p, [][3]int{{1, 1, 1}, {2, 2, 2}})
}
