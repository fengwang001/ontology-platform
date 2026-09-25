package ontology

import "testing"

// TestPartitionIsolation verifies that each partition ranks from 1,
// the empty string is a valid partition key, nil keys are rejected
// and counted, and partitions come out in lexicographic key order.
func TestPartitionIsolation(t *testing.T) {
	rows := []Row{
		{Partition: StringPtr("b"), Value: 1, ID: "b1"},
		{Partition: StringPtr("a"), Value: 7, ID: "a1"},
		{Partition: StringPtr(""), Value: 3, ID: "e1"},
		{Partition: StringPtr("b"), Value: 1, ID: "b2"},
		{Partition: StringPtr("a"), Value: 5, ID: "a2"},
		{Partition: nil, Value: 9, ID: "n1"},
		{Partition: nil, Value: 9, ID: "n2"},
		{Partition: StringPtr(""), Value: 3, ID: "e2"},
	}
	sum := Compute(rows, Options{})

	if sum.SkippedNilPartition != 2 {
		t.Fatalf("SkippedNilPartition = %d, want 2", sum.SkippedNilPartition)
	}
	if len(sum.Rows) != 6 {
		t.Fatalf("got %d ranked rows, want 6", len(sum.Rows))
	}

	// Partition order must be "", "a", "b" (lexicographic).
	wantParts := []string{"", "", "a", "a", "b", "b"}
	for i, p := range wantParts {
		if sum.Rows[i].Partition != p {
			t.Fatalf("row %d partition = %q, want %q", i, sum.Rows[i].Partition, p)
		}
	}

	// Each partition restarts all three counters at 1.
	for i, r := range sum.Rows {
		if i%2 == 0 && (r.RowNumber != 1 || r.Rank != 1 || r.DenseRank != 1) {
			t.Errorf("partition %q does not restart at 1: %+v", r.Partition, r)
		}
	}

	// Empty-string partition: tied 3,3 -> rank 1,1 dense 1,1 rn 1,2.
	assertRanks(t, sum.Rows[0:2], []wantRank{{1, 1, 1}, {2, 1, 1}})
	// Partition "a": values 5,7 -> 1,2 across all columns.
	assertRanks(t, sum.Rows[2:4], []wantRank{{1, 1, 1}, {2, 2, 2}})
	// Partition "b": tied 1,1.
	assertRanks(t, sum.Rows[4:6], []wantRank{{1, 1, 1}, {2, 1, 1}})
}

// TestPartitionOrderIsSorted checks lexicographic partition ordering
// independent of first-seen order in the input.
func TestPartitionOrderIsSorted(t *testing.T) {
	rows := []Row{
		{Partition: StringPtr("zeta"), Value: 1, ID: "1"},
		{Partition: StringPtr("alpha"), Value: 1, ID: "2"},
		{Partition: StringPtr("mid"), Value: 1, ID: "3"},
	}
	sum := Compute(rows, Options{})
	want := []string{"alpha", "mid", "zeta"}
	got := []string{sum.Rows[0].Partition, sum.Rows[1].Partition, sum.Rows[2].Partition}
	for i, p := range want {
		if got[i] != p {
			t.Fatalf("partition order = %q, want %q", got, want)
		}
	}
}
