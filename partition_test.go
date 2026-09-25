package ontology

import "testing"

func TestPartitionIsolationAndOrder(t *testing.T) {
	rows := []Row{
		{Partition: ptr("b"), Value: 5, ID: "b1"},
		{Partition: ptr("a"), Value: 1, ID: "a1"},
		{Partition: ptr(""), Value: 9, ID: "e1"},
		{Partition: ptr("a"), Value: 2, ID: "a2"},
		{Partition: ptr("b"), Value: 5, ID: "b0"},
		{Partition: nil, Value: 1, ID: "nil1"},
		{Partition: nil, Value: 7, ID: "nil2"},
	}

	res := Rank(rows, Options{})

	if res.SkippedNilPartition != 2 {
		t.Fatalf("SkippedNilPartition=%d, want 2", res.SkippedNilPartition)
	}
	if res.Skipped() != 2 {
		t.Fatalf("Skipped()=%d, want 2", res.Skipped())
	}

	// Empty string sorts before everything; otherwise lexicographic.
	want := []expectedRank{
		{"e1", 1, 1, 1}, // partition ""
		{"a1", 1, 1, 1}, // partition "a", numbering restarts
		{"a2", 2, 2, 2},
		{"b0", 1, 1, 1}, // partition "b", tie ordered by ID
		{"b1", 2, 1, 1},
	}
	assertRanks(t, res.Rows, want)

	// Partition boundaries must never leak into a neighboring group.
	for i, r := range res.Rows {
		wantKey := []string{"", "a", "a", "b", "b"}[i]
		if got := *r.Row.Partition; got != wantKey {
			t.Errorf("row %d partition=%q, want %q", i, got, wantKey)
		}
	}
}

func TestEmptyPartitionKeyIsValid(t *testing.T) {
	res := Rank([]Row{
		{Partition: ptr(""), Value: 3, ID: "x"},
		{Partition: ptr(""), Value: 2, ID: "y"},
	}, Options{})
	if res.Skipped() != 0 || len(res.Rows) != 2 {
		t.Fatalf("empty-string partition rows were rejected: %+v", res)
	}
	assertRanks(t, res.Rows, []expectedRank{
		{"y", 1, 1, 1},
		{"x", 2, 2, 2},
	})
}
