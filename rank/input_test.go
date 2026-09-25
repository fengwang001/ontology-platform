package rank_test

import (
	"testing"

	"ontology/rank"
)

// snapshotRows deep-copies the field values of every row so the input
// can be compared field by field after the call.
func snapshotRows(rows []rank.Row) []rank.Row {
	out := make([]rank.Row, len(rows))
	for i, r := range rows {
		out[i] = r
		if r.Partition != nil {
			key := *r.Partition
			out[i].Partition = &key
		}
	}
	return out
}

// Compute must not modify the input slice or any Row in it, and the
// returned slices must be freshly allocated (mutating them must not
// touch the caller's data).
func TestInputNotModified(t *testing.T) {
	rows := makeRows("b", []string{"r1", "r2", "r3"}, []float64{3, 1, 2})
	rows = append(rows, makeRows("a", []string{"r4", "r5"}, []float64{2, 2})...)
	before := snapshotRows(rows)

	res := rank.Compute(rows, rank.Options{})
	if len(res.Partitions) != 2 {
		t.Fatalf("partitions = %d, want 2", len(res.Partitions))
	}

	// Field-by-field comparison, including pointer targets.
	if len(rows) != len(before) {
		t.Fatalf("input length changed: %d -> %d", len(before), len(rows))
	}
	for i := range before {
		got, want := rows[i], before[i]
		if got.ID != want.ID || got.Value != want.Value {
			t.Errorf("row %d: got {%v %v}, want {%v %v}", i, got.ID, got.Value, want.ID, want.Value)
		}
		if (got.Partition == nil) != (want.Partition == nil) {
			t.Errorf("row %d: partition nil-ness changed", i)
		} else if got.Partition != nil && *got.Partition != *want.Partition {
			t.Errorf("row %d: partition = %q, want %q", i, *got.Partition, *want.Partition)
		}
	}

	// The result slices are fresh: scribbling on them must not change
	// the caller's rows.
	for _, p := range res.Partitions {
		for j := range p.Rows {
			p.Rows[j].Row.Value = -999
			p.Rows[j].Row.ID = "scribbled"
			p.Rows[j].RowNumber = -1
		}
	}
	for i := range before {
		if rows[i].ID != before[i].ID || rows[i].Value != before[i].Value {
			t.Errorf("row %d mutated via result aliasing", i)
		}
		if *rows[i].Partition != *before[i].Partition {
			t.Errorf("row %d partition mutated via result aliasing", i)
		}
	}
}

// Even the row order inside the input slice must survive the call.
func TestInputOrderPreserved(t *testing.T) {
	rows := makeRows("p",
		[]string{"z", "y", "x", "w"},
		[]float64{4, 3, 2, 1})
	wantIDs := make([]string, len(rows))
	for i, r := range rows {
		wantIDs[i] = r.ID
	}
	_ = rank.Compute(rows, rank.Options{})
	for i, r := range rows {
		if r.ID != wantIDs[i] {
			t.Fatalf("input reordered at %d: got %s, want %s", i, r.ID, wantIDs[i])
		}
	}
}
