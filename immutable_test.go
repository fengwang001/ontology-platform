package ontology

import (
	"math"
	"reflect"
	"testing"
)

func TestInputNotModifiedAndOutputNew(t *testing.T) {
	keyP, keyQ := "p", "q"
	rows := []Row{
		{Partition: &keyQ, Value: 2, ID: "q2"},
		{Partition: &keyP, Value: 3, ID: "p3"},
		{Partition: &keyP, Value: 1, ID: "p1"},
		{Partition: &keyQ, Value: 2, ID: "q1"},
	}

	snapshot := make([]Row, len(rows))
	copy(snapshot, rows)

	res := Rank(rows, Options{})

	// The slice order and every field of every Row must be unchanged.
	if !reflect.DeepEqual(rows, snapshot) {
		t.Fatalf("input rows were modified\nbefore=%v\nafter =%v", snapshot, rows)
	}

	// Partition pointers and contents must be the exact same references,
	// while result rows must not alias the input slice storage.
	if res.Rows[0].Row.Partition != rows[2].Partition {
		t.Errorf("partition pointer unexpectedly copied by value")
	}
	if len(res.Rows) != 4 {
		t.Fatalf("want 4 ranked rows, got %d", len(res.Rows))
	}

	// Output is sorted even though input is not, proving the sort happened
	// on copies while the input order stayed intact.
	if res.Rows[0].Row.ID != "p1" {
		t.Fatalf("result unexpectedly ordered as input: first id=%s", res.Rows[0].Row.ID)
	}

	// Mutating the result must not change the caller's rows.
	res.Rows[0].Row.ID = "mutated"
	if rows[2].ID != "p1" {
		t.Fatalf("result aliases input storage: rows[2].ID=%q", rows[2].ID)
	}
}

func TestNilAndNaNDoNotMutateInput(t *testing.T) {
	key := "p"
	rows := []Row{
		{Partition: nil, Value: 1, ID: "nil"},
		{Partition: &key, Value: math.NaN(), ID: "nan"},
	}
	snapshot := make([]Row, len(rows))
	copy(snapshot, rows)

	res := Rank(rows, Options{})
	if res.Skipped() != 2 {
		t.Fatalf("want 2 skipped, got %d", res.Skipped())
	}
	for i := range rows {
		same := rows[i].Partition == snapshot[i].Partition &&
			rows[i].ID == snapshot[i].ID &&
			(rows[i].Value == snapshot[i].Value ||
				(math.IsNaN(rows[i].Value) && math.IsNaN(snapshot[i].Value)))
		if !same {
			t.Fatalf("row %d modified while skipping: before=%v after=%v",
				i, snapshot[i], rows[i])
		}
	}
}
