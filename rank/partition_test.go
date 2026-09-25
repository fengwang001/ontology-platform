package rank

import "testing"

// Each partition ranks independently from 1, and partitions appear in
// lexicographic key order. The empty string is a valid partition key.
func TestPartitionIsolationAndOrder(t *testing.T) {
	rows := []Row{
		{Partition: strptr("b"), Value: 1, ID: "b1"},
		{Partition: strptr("a"), Value: 10, ID: "a1"},
		{Partition: strptr(""), Value: 5, ID: "e1"},
		{Partition: strptr("b"), Value: 2, ID: "b2"},
		{Partition: strptr("a"), Value: 10, ID: "a2"},
		{Partition: strptr(""), Value: 4, ID: "e2"},
	}
	res := Rank(rows, Config{})
	if res.Skipped != 0 {
		t.Fatalf("Skipped = %d, want 0", res.Skipped)
	}

	wantPartitions := []string{"", "", "a", "a", "b", "b"}
	if len(res.Rows) != len(wantPartitions) {
		t.Fatalf("len(Rows) = %d, want %d", len(res.Rows), len(wantPartitions))
	}
	for i, p := range wantPartitions {
		if *res.Rows[i].Row.Partition != p {
			t.Fatalf("row %d: partition = %q, want %q (lexicographic order)",
				i, *res.Rows[i].Row.Partition, p)
		}
	}

	// Within each partition, ranking restarts at 1.
	want := map[string]want3{
		"e2": {1, 1, 1}, // "" partition, value 4
		"e1": {2, 2, 2}, // "" partition, value 5
		"a1": {1, 1, 1}, // "a" partition, value 10, tie -> ID order
		"a2": {2, 1, 1},
		"b1": {1, 1, 1}, // "b" partition, value 1
		"b2": {2, 2, 2},
	}
	for _, rr := range res.Rows {
		w, ok := want[rr.Row.ID]
		if !ok {
			t.Fatalf("unexpected row id %q", rr.Row.ID)
		}
		if rr.RowNumber != w.rowNumber || rr.Rank != w.rank || rr.DenseRank != w.dense {
			t.Errorf("id %s: got (%d,%d,%d), want (%d,%d,%d)",
				rr.Row.ID, rr.RowNumber, rr.Rank, rr.DenseRank,
				w.rowNumber, w.rank, w.dense)
		}
	}
}

// Rows with a nil partition key are rejected and counted.
func TestNilPartitionSkipped(t *testing.T) {
	rows := []Row{
		{Partition: nil, Value: 1, ID: "bad1"},
		{Partition: strptr("p"), Value: 2, ID: "ok1"},
		{Partition: nil, Value: 3, ID: "bad2"},
	}
	res := Rank(rows, Config{})
	if res.Skipped != 2 || res.SkippedNilPartition != 2 {
		t.Fatalf("Skipped=%d NilPartition=%d, want 2 and 2",
			res.Skipped, res.SkippedNilPartition)
	}
	if len(res.Rows) != 1 || res.Rows[0].Row.ID != "ok1" {
		t.Fatalf("Rows = %v, want only ok1", res.Rows)
	}
	if res.Rows[0].RowNumber != 1 || res.Rows[0].Rank != 1 || res.Rows[0].DenseRank != 1 {
		t.Fatalf("ok1 columns = (%d,%d,%d), want (1,1,1)",
			res.Rows[0].RowNumber, res.Rows[0].Rank, res.Rows[0].DenseRank)
	}
}

// Empty input yields an empty, non-nil result with zero skips.
func TestEmptyInput(t *testing.T) {
	res := Rank(nil, Config{})
	if res.Skipped != 0 || len(res.Rows) != 0 {
		t.Fatalf("got %+v, want empty result", res)
	}
	if res.Rows == nil {
		t.Fatal("Rows is nil, want newly allocated empty slice")
	}
}
