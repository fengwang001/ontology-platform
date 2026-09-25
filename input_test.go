package ontology

import "testing"

// snapshotRows deep-copies a row slice for later comparison.
func snapshotRows(rows []Row) []Row {
	out := make([]Row, len(rows))
	for i, r := range rows {
		out[i] = r
		if r.Partition != nil {
			p := *r.Partition
			out[i].Partition = &p
		}
	}
	return out
}

// TestInputNotModified: the input slice and every row in it must be
// field-by-field identical after Compute, and the result must be a
// freshly allocated slice.
func TestInputNotModified(t *testing.T) {
	rows := []Row{
		{Partition: StringPtr("b"), Value: 20, ID: "x"},
		{Partition: StringPtr("a"), Value: 10, ID: "y"},
		{Partition: StringPtr("b"), Value: 20, ID: "z"},
		{Partition: nil, Value: 1, ID: "n"},
	}
	before := snapshotRows(rows)
	beforePtrs := make([]*string, len(rows))
	for i, r := range rows {
		beforePtrs[i] = r.Partition
	}

	sum := Compute(rows, Options{})
	sumDesc := Compute(rows, Options{Descending: true})

	if len(rows) != len(before) {
		t.Fatalf("input length changed: %d -> %d", len(before), len(rows))
	}
	for i := range rows {
		if rows[i].Value != before[i].Value || rows[i].ID != before[i].ID {
			t.Errorf("row %d mutated: %+v -> %+v", i, before[i], rows[i])
		}
		if rows[i].Partition != beforePtrs[i] {
			t.Errorf("row %d partition pointer changed", i)
		}
		if rows[i].Partition != nil && *rows[i].Partition != *before[i].Partition {
			t.Errorf("row %d partition value mutated", i)
		}
	}

	// The returned slice must not alias the input slice's backing
	// array: mutating the result must not touch the input.
	if len(sum.Rows) > 0 {
		sum.Rows[0].Row.ID = "MUTATED"
		sumDesc.Rows[0].Row.Value = -999
		for _, r := range rows {
			if r.ID == "MUTATED" || r.Value == -999 {
				t.Fatal("result slice aliases input storage")
			}
		}
	}
}
