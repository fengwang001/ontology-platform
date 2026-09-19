package topk

import (
	"math"
	"reflect"
	"testing"
)

func TestNaNRejectedAndCounted(t *testing.T) {
	s, err := New(3, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 1.0)
	s.Push("nan1", math.NaN())
	s.Push("b", 2.0)
	s.Push("nan2", math.NaN())
	if got := s.Skipped(); got != 2 {
		t.Fatalf("Skipped = %d, want 2", got)
	}
	if got := s.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	want := []string{"b", "a"}
	if got := ids(s.Snapshot()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot = %v, want %v", got, want)
	}
}

func TestNaNDoesNotOverwriteExistingID(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 1.0)
	s.Push("a", math.NaN())
	got := s.Snapshot()
	if len(got) != 1 || got[0].Score != 1.0 {
		t.Fatalf("NaN overwrite corrupted state: %v", got)
	}
	if s.Skipped() != 1 {
		t.Fatalf("Skipped = %d, want 1", s.Skipped())
	}
}

func TestInfinitiesAreValidScores(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("inf", math.Inf(1))
	s.Push("mid", 0.0)
	s.Push("neg", math.Inf(-1))
	got := ids(s.Snapshot())
	want := []string{"inf", "mid"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Desc with infinities = %v, want %v", got, want)
	}

	a, err := New(2, Asc)
	if err != nil {
		t.Fatal(err)
	}
	a.Push("inf", math.Inf(1))
	a.Push("mid", 0.0)
	a.Push("neg", math.Inf(-1))
	got = ids(a.Snapshot())
	want = []string{"neg", "mid"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Asc with infinities = %v, want %v", got, want)
	}
}
