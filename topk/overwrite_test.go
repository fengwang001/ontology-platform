package topk

import (
	"reflect"
	"testing"
)

func TestOverwriteDropsOutOfTopK(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 10.0)
	s.Push("b", 9.0)
	s.Push("c", 8.0) // evicted immediately: 8 < 9
	s.Push("a", 1.0) // was rank 1, now worst of the retained set
	got := ids(s.Snapshot())
	want := []string{"b", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after overwrite = %v, want %v", got, want)
	}
	// The next contender evicts "a": the overwrite made it drop out.
	s.Push("d", 5.0)
	got = ids(s.Snapshot())
	want = []string{"b", "d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after eviction = %v, want %v", got, want)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}

func TestOverwriteImprovesIntoTopK(t *testing.T) {
	s, err := New(2, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 1.0)
	s.Push("b", 2.0)
	s.Push("c", 3.0)
	s.Push("c", 0.5) // was evicted? no: c never entered; push new
	got := ids(s.Snapshot())
	want := []string{"c", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after improve = %v, want %v", got, want)
	}
}

func TestOverwriteExistingRetainedID(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 5.0)
	s.Push("b", 4.0)
	s.Push("b", 6.0) // b overtakes a
	got := ids(s.Snapshot())
	want := []string{"b", "a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("re-rank = %v, want %v", got, want)
	}
}

func TestNoDuplicateIDsAfterManyOverwrites(t *testing.T) {
	s, err := New(3, Desc)
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 50; round++ {
		for _, id := range []string{"x", "y", "z", "w"} {
			s.Push(id, float64(round))
		}
	}
	seen := map[string]bool{}
	for _, it := range s.Snapshot() {
		if seen[it.ID] {
			t.Fatalf("duplicate ID %q in snapshot", it.ID)
		}
		seen[it.ID] = true
	}
	if s.Len() > 3 {
		t.Fatalf("Len = %d exceeds K=3", s.Len())
	}
}
