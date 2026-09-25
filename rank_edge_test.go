package ontology

import "testing"

func TestEmptyAndNilInput(t *testing.T) {
	for _, in := range [][]Row{nil, {}} {
		res := Rank(in, Ascending)
		if res.Skipped != 0 {
			t.Fatalf("Skipped=%d want 0", res.Skipped)
		}
		if len(res.Rows) != 0 {
			t.Fatalf("expected no rows, got %d", len(res.Rows))
		}
}
}

func TestSkippedOnlyPartitions(t *testing.T) {
	// When every row is rejected, no partitions exist and order is empty.
	rows := []Row{
		{Partition: nil, Value: 1, ID: "x"},
		{Partition: strptr("p"), Value: nanVal(), ID: "y"},
	}
	res := Rank(rows, Ascending)
	if res.Skipped != 2 || len(res.Rows) != 0 {
		t.Fatalf("got %+v", res)
	}
}
