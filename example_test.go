package ontology

import "testing"

func TestSpecExample(t *testing.T) {
	a, err := NewRangeAggregator(2, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []Row{{Key: []byte("a"), TS: 5, Val: 10}, {Key: []byte("a"), TS: 3, Val: 1}, {Key: []byte("a"), TS: 5, Val: 20}, {Key: []byte("b"), TS: 4, Val: 7}} {
		if _, status, err := a.Insert(row); err != nil || status != "buffered" {
			t.Fatalf("Insert %+v: status=%q err=%v", row, status, err)
		}
	}

	outputs, err := a.Advance(4)
	if err != nil {
		t.Fatal(err)
	}
	want := []Output{{Key: []byte("a"), TS: 3, Val: 1, Sum: 1, Cnt: 1, Max: 1}, {Key: []byte("b"), TS: 4, Val: 7, Sum: 7, Cnt: 1, Max: 7}}
	assertOutputs(t, outputs, want)
	if got := a.Retained(); got != 2 {
		t.Fatalf("Retained=%d want 2", got)
	}

	outputs, err = a.Advance(5)
	if err != nil {
		t.Fatal(err)
	}
	want = []Output{{Key: []byte("a"), TS: 5, Val: 10, Sum: 31, Cnt: 3, Max: 20}, {Key: []byte("a"), TS: 5, Val: 20, Sum: 31, Cnt: 3, Max: 20}}
	assertOutputs(t, outputs, want)
	if got := a.Retained(); got != 3 {
		t.Fatalf("Retained=%d want 3", got)
	}

	if _, status, err := a.Insert(Row{Key: []byte("a"), TS: 5, Val: 99}); err != nil || status != "late" {
		t.Fatalf("equal wm insert: status=%q err=%v", status, err)
	}
	if _, status, err := a.Insert(Row{Key: []byte("a"), TS: 6, Val: 4}); err != nil || status != "buffered" {
		t.Fatalf("wm+1 insert: status=%q err=%v", status, err)
	}
	outputs, err = a.Advance(8)
	if err != nil {
		t.Fatal(err)
	}
	want = []Output{{Key: []byte("a"), TS: 6, Val: 4, Sum: 34, Cnt: 3, Max: 20}}
	assertOutputs(t, outputs, want)
	if got := a.Retained(); got != 0 {
		t.Fatalf("Retained=%d want 0", got)
	}
	if _, err := a.Advance(7); err != ErrInvalidArgument {
		t.Fatalf("regression err=%v want ErrInvalidArgument", err)
	}
}

func TestSupplementExample(t *testing.T) {
	a, err := NewRangeAggregator(2, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := a.Insert(Row{Key: []byte("a"), TS: 5, Val: 10}); err != nil || status != "buffered" {
		t.Fatal(status, err)
	}
	outputs, err := a.Advance(5)
	if err != nil {
		t.Fatal(err)
	}
	assertOutputs(t, outputs, []Output{{Key: []byte("a"), TS: 5, Val: 10, Sum: 10, Cnt: 1, Max: 10}})

	out, status, err := a.Insert(Row{Key: []byte("a"), TS: 4, Val: 3})
	if err != nil || status != "supplement" {
		t.Fatal(status, err)
	}
	assertOutputs(t, []Output{out}, []Output{{Key: []byte("a"), TS: 4, Val: 3, Sum: 3, Cnt: 1, Max: 3}})

	out, status, err = a.Insert(Row{Key: []byte("a"), TS: 5, Val: 7})
	if err != nil || status != "supplement" {
		t.Fatal(status, err)
	}
	assertOutputs(t, []Output{out}, []Output{{Key: []byte("a"), TS: 5, Val: 7, Sum: 20, Cnt: 3, Max: 10}})

	if _, status, err := a.Insert(Row{Key: []byte("a"), TS: 3, Val: 1}); err != nil || status != "late" {
		t.Fatal(status, err)
	}
	if outputs, err := a.Advance(8); err != nil || len(outputs) != 0 {
		t.Fatal(outputs, err)
	}
	if got := a.Retained(); got != 2 {
		t.Fatalf("Retained=%d want 2", got)
	}
}

func assertOutputs(t *testing.T, got, want []Output) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("outputs len=%d want %d, got=%+v", len(got), len(want), got)
	}
	for i := range want {
		if string(got[i].Key) != string(want[i].Key) ||
			got[i].TS != want[i].TS || got[i].Val != want[i].Val ||
			got[i].Sum != want[i].Sum || got[i].Cnt != want[i].Cnt ||
			got[i].Max != want[i].Max {
			t.Fatalf("output %d=%+v want %+v", i, got[i], want[i])
		}
	}
}
