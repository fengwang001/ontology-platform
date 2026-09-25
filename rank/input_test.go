package rank

import (
	"reflect"
	"testing"
)

// Rank must not modify the input slice or any Row in it, and the
// returned slice must be independently allocated.
func TestInputNotModified(t *testing.T) {
	p1, p2 := "b", "a"
	rows := []Row{
		{Partition: &p1, Value: 3, ID: "r3"},
		{Partition: &p2, Value: 1, ID: "r1"},
		{Partition: &p1, Value: 2, ID: "r2"},
		{Partition: &p2, Value: 1, ID: "r0"},
	}
	// Deep snapshot: header (len/cap/pointer order) plus every field.
	snapshot := make([]Row, len(rows))
	copy(snapshot, rows)
	firstElemPtr := &rows[0]

	res := Rank(rows, Config{})

	if len(rows) != len(snapshot) {
		t.Fatalf("input length changed: %d -> %d", len(snapshot), len(rows))
	}
	if &rows[0] != firstElemPtr {
		t.Fatal("input slice backing array changed")
	}
	for i := range rows {
		if rows[i] != snapshot[i] {
			t.Errorf("input row %d modified: was %+v, now %+v", i, snapshot[i], rows[i])
		}
		if rows[i].Partition != snapshot[i].Partition {
			t.Errorf("input row %d partition pointer changed", i)
		}
	}

	// The result must be a fresh slice: mutating it must not affect input.
	if len(res.Rows) == 0 {
		t.Fatal("empty result")
	}
	res.Rows[0].RowNumber = -999
	res.Rows[0].Row.ID = "mutated"
	for i := range rows {
		if rows[i] != snapshot[i] {
			t.Fatalf("mutating result affected input row %d", i)
		}
	}
}

// Ranking the same logical input twice must not alias memory between
// the two results.
func TestResultsIndependent(t *testing.T) {
	rows := baseRows()
	r1 := Rank(rows, Config{})
	r2 := Rank(rows, Config{})
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("two runs over the same input differ")
	}
	r1.Rows[0].RowNumber = -1
	if r2.Rows[0].RowNumber == -1 {
		t.Fatal("results share backing memory")
	}
}
