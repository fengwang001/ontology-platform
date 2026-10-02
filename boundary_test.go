package ontology

import "testing"

func TestLatenessBoundariesAndFullBuffer(t *testing.T) {
	a, _ := NewRangeAggregator(0, 2, 1)
	mustInsert(t, a, Row{Key: []byte("a"), TS: 2, Val: 1}, "buffered")
	if outputs, err := a.Advance(2); err != nil || len(outputs) != 1 {
		t.Fatal(outputs, err)
	}
	mustInsert(t, a, Row{Key: []byte("b"), TS: 10, Val: 1}, "buffered")
	mustInsert(t, a, Row{Key: []byte("a"), TS: 0, Val: 1}, "late")
	out, _ := mustInsert(t, a, Row{Key: []byte("a"), TS: 1, Val: 2}, "supplement")
	if out.Sum != 2 || out.Cnt != 1 || out.Max != 2 {
		t.Fatalf("supplement frame=%+v", out)
	}
	mustInsert(t, a, Row{Key: []byte("a"), TS: 2, Val: 3}, "supplement")
	if a.supplemented != 2 || a.retainedCount != 3 {
		t.Fatalf("supplemented=%d retained=%d", a.supplemented, a.retainedCount)
	}
	_, _, err := a.Insert(Row{Key: []byte("c"), TS: 11, Val: 1})
	if err != ErrInvalidArgument {
		t.Fatalf("full buffer err=%v", err)
	}

	b, _ := NewRangeAggregator(0, 0, 10)
	mustInsert(t, b, Row{Key: []byte("a"), TS: 5, Val: 1}, "buffered")
	if _, err := b.Advance(5); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, b, Row{Key: []byte("a"), TS: 5, Val: 1}, "late")
	mustInsert(t, b, Row{Key: []byte("a"), TS: 6, Val: 1}, "buffered")
}

func TestTieAndFrameBoundaries(t *testing.T) {
	a, _ := NewRangeAggregator(2, 0, 100)
	mustInsert(t, a, Row{Key: []byte("a"), TS: 1, Val: 1}, "buffered")
	mustInsert(t, a, Row{Key: []byte("a"), TS: 3, Val: 10}, "buffered")
	mustInsert(t, a, Row{Key: []byte("a"), TS: 3, Val: 20}, "buffered")
	mustInsert(t, a, Row{Key: []byte("b"), TS: 3, Val: 7}, "buffered")
	outputs, err := a.Advance(3)
	if err != nil {
		t.Fatal(err)
	}
	assertOutputs(t, outputs, []Output{
		{Key: []byte("a"), TS: 1, Val: 1, Sum: 1, Cnt: 1, Max: 1},
		{Key: []byte("a"), TS: 3, Val: 10, Sum: 31, Cnt: 3, Max: 20},
		{Key: []byte("a"), TS: 3, Val: 20, Sum: 31, Cnt: 3, Max: 20},
		{Key: []byte("b"), TS: 3, Val: 7, Sum: 7, Cnt: 1, Max: 7},
	})

	mustInsert(t, a, Row{Key: []byte("a"), TS: 4, Val: 4}, "buffered")
	outputs, err = a.Advance(4)
	if err != nil {
		t.Fatal(err)
	}
	assertOutputs(t, outputs, []Output{{Key: []byte("a"), TS: 4, Val: 4, Sum: 34, Cnt: 3, Max: 20}})
}

func TestRZeroIncludesOnlyTies(t *testing.T) {
	a, _ := NewRangeAggregator(0, 0, 100)
	mustInsert(t, a, Row{Key: []byte("a"), TS: 2, Val: 1}, "buffered")
	mustInsert(t, a, Row{Key: []byte("a"), TS: 3, Val: 10}, "buffered")
	mustInsert(t, a, Row{Key: []byte("a"), TS: 3, Val: 20}, "buffered")
	mustInsert(t, a, Row{Key: []byte("a"), TS: 4, Val: 100}, "buffered")
	outputs, err := a.Advance(4)
	if err != nil {
		t.Fatal(err)
	}
	assertOutputs(t, outputs, []Output{
		{Key: []byte("a"), TS: 2, Val: 1, Sum: 1, Cnt: 1, Max: 1},
		{Key: []byte("a"), TS: 3, Val: 10, Sum: 30, Cnt: 2, Max: 20},
		{Key: []byte("a"), TS: 3, Val: 20, Sum: 30, Cnt: 2, Max: 20},
		{Key: []byte("a"), TS: 4, Val: 100, Sum: 100, Cnt: 1, Max: 100},
	})
}

func TestMaxFallsBackAndCleanupBoundary(t *testing.T) {
	a, _ := NewRangeAggregator(1, 0, 100)
	mustInsert(t, a, Row{Key: []byte("a"), TS: 1, Val: 10}, "buffered")
	mustInsert(t, a, Row{Key: []byte("a"), TS: 2, Val: 1}, "buffered")
	outputs, err := a.Advance(2)
	if err != nil {
		t.Fatal(err)
	}
	assertOutputs(t, outputs, []Output{
		{Key: []byte("a"), TS: 1, Val: 10, Sum: 10, Cnt: 1, Max: 10},
		{Key: []byte("a"), TS: 2, Val: 1, Sum: 11, Cnt: 2, Max: 10},
	})
	if got := a.Retained(); got != 1 {
		t.Fatalf("Retained=%d want 1", got)
	}
	mustInsert(t, a, Row{Key: []byte("a"), TS: 3, Val: 5}, "buffered")
	outputs, err = a.Advance(3)
	if err != nil {
		t.Fatal(err)
	}
	assertOutputs(t, outputs, []Output{{Key: []byte("a"), TS: 3, Val: 5, Sum: 6, Cnt: 2, Max: 5}})
	if got := a.Retained(); got != 1 {
		t.Fatalf("Retained=%d want 1", got)
	}
	if outputs, err := a.Advance(3); err != nil || outputs != nil {
		t.Fatalf("same watermark outputs=%v err=%v", outputs, err)
	}
	if _, err := a.Advance(2); err != ErrInvalidArgument {
		t.Fatalf("regression err=%v", err)
	}
}

func TestInvalidOperationsDoNotChangeState(t *testing.T) {
	a, _ := NewRangeAggregator(1, 1, 1)
	if _, _, err := a.Insert(Row{Key: nil, TS: 0, Val: 0}); err != ErrInvalidArgument {
		t.Fatal(err)
	}
	if _, err := a.Advance(-1); err != ErrInvalidArgument {
		t.Fatal(err)
	}
	if got := a.Retained(); got != 0 || a.wm != -1 || a.seq != 0 || a.lateDropped != 0 || a.supplemented != 0 {
		t.Fatalf("state changed: retained=%d wm=%d seq=%d late=%d supplement=%d", got, a.wm, a.seq, a.lateDropped, a.supplemented)
	}
}

func mustInsert(t *testing.T, a *RangeAggregator, row Row, wantStatus string) (Output, string) {
	t.Helper()
	out, status, err := a.Insert(row)
	if err != nil {
		t.Fatalf("Insert %+v unexpected err=%v", row, err)
	}
	if status != wantStatus {
		t.Fatalf("Insert %+v status=%s want %s", row, status, wantStatus)
	}
	return out, status
}
