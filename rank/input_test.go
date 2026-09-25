package rank

import (
	"reflect"
	"testing"
)

// TestInputNotModified verifies that neither the input slice nor any
// row in it changes after Compute, and that the returned slice is
// freshly allocated (mutating it must not touch the input).
func TestInputNotModified(t *testing.T) {
	pA, pB := "a", "b"
	rows := []Row{
		{Partition: &pA, Value: 20, ID: 3},
		{Partition: &pB, Value: 10, ID: 1},
		{Partition: &pA, Value: 20, ID: 2},
		{Partition: &pB, Value: 30, ID: 4},
	}
	snapshot := make([]Row, len(rows))
	copy(snapshot, rows)
	res := Compute(rows, Options{})
	if !reflect.DeepEqual(rows, snapshot) {
		t.Fatalf("input slice modified:\n got %v\nwant %v", rows, snapshot)
	}
	for i := range rows {
		if rows[i].Partition != snapshot[i].Partition {
			t.Errorf("row %d: Partition pointer changed", i)
		}
		if rows[i].Value != snapshot[i].Value || rows[i].ID != snapshot[i].ID {
			t.Errorf("row %d: fields changed", i)
		}
	}
	if len(res.Rows) == 0 {
		t.Fatal("empty result")
	}
	// Scribble on the result; the input must be unaffected.
	res.Rows[0].Row.Value = -999
	res.Rows[0].Row.ID = -999
	res.Rows[0].Rank = -999
	if !reflect.DeepEqual(rows, snapshot) {
		t.Fatal("mutating result rows leaked into the input")
	}
}

// TestResultIsFreshSlice: two calls return independent slices.
func TestResultIsFreshSlice(t *testing.T) {
	rows := rowsFromValues("p", []float64{2, 1, 1})
	first := Compute(rows, Options{})
	second := Compute(rows, Options{})
	if len(first.Rows) == 0 || len(second.Rows) == 0 {
		t.Fatal("empty result")
	}
	if &first.Rows[0] == &second.Rows[0] {
		t.Fatal("two calls share backing storage")
	}
	first.Rows[0].Rank = -1
	if second.Rows[0].Rank == -1 {
		t.Fatal("mutating first result affected second result")
	}
}
