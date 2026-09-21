package ontology

import (
	"reflect"
	"testing"
)

func buildSnapshotSelector(t *testing.T) *Selector {
	t.Helper()
	s, err := New(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "b", "s": 1.0, "t": "x"})
	s.Add(map[string]any{"g": "a", "s": 2.0, "t": "x"})
	s.Add(map[string]any{"g": "a", "s": 3.0, "t": "x"})
	s.Add(map[string]any{"s": 4.0, "t": "x"}) // missing group key
	return s
}

func TestSnapshotIsRepeatableAndOrdered(t *testing.T) {
	s := buildSnapshotSelector(t)
	first := s.Snapshot()
	for i := 0; i < 5; i++ {
		if got := s.Snapshot(); !reflect.DeepEqual(got, first) {
			t.Fatalf("snapshot %d differs from the first", i)
		}
	}
	// Groups sorted: Missing first, then "a", then "b".
	want := []GroupKey{
		{Kind: KeyMissing},
		{Kind: KeyValue, Value: "a"},
		{Kind: KeyValue, Value: "b"},
	}
	if len(first) != len(want) {
		t.Fatalf("want %d groups, got %d", len(want), len(first))
	}
	for i, k := range want {
		if first[i].Group != k {
			t.Fatalf("group %d: want %v, got %v", i, k, first[i].Group)
		}
	}
	// Within group "a": best score first.
	if first[1].Rows[0]["s"] != 3.0 || first[1].Rows[1]["s"] != 2.0 {
		t.Fatalf("group a rows out of order: %v", first[1].Rows)
	}
}

func TestSnapshotSlicesAreIsolated(t *testing.T) {
	s := buildSnapshotSelector(t)
	snap := s.Snapshot()
	// Mutating the returned slices must not disturb internal state.
	snap[1].Rows[0] = map[string]any{"g": "a", "s": -999.0}
	snap[1].Rows = nil
	again := s.Snapshot()
	if again[1].Rows[0]["s"] != 3.0 {
		t.Fatalf("internal state leaked into snapshot: %v", again[1].Rows[0])
	}
	if len(again[1].Rows) != 2 {
		t.Fatalf("snapshot mutation affected later snapshots: %v", again[1].Rows)
	}
}
