package topk

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

func ids(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestDescTiesBreakByIDAscending(t *testing.T) {
	s, err := New(3, Desc)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c", "a", "b"} {
		s.Push(id, 1.0)
	}
	s.Push("z", 0.5)
	got := ids(s.Snapshot())
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Desc tie order = %v, want %v", got, want)
	}
}

func TestAscTiesBreakByIDAscending(t *testing.T) {
	s, err := New(3, Asc)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c", "a", "b"} {
		s.Push(id, 1.0)
	}
	s.Push("z", 2.0)
	got := ids(s.Snapshot())
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Asc tie order = %v, want %v", got, want)
	}
}

func TestAscDoesNotInvertTieBreak(t *testing.T) {
	s, err := New(4, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("b", 5.0)
	s.Push("a", 5.0)
	s.Push("d", 1.0)
	s.Push("c", 1.0)
	got := ids(s.Snapshot())
	want := []string{"c", "d", "a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Asc mixed order = %v, want %v", got, want)
	}
}

func TestSignedZerosAreEqual(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("b", math.Copysign(0, -1))
	s.Push("a", 0.0)
	got := ids(s.Snapshot())
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signed zero order = %v, want %v", got, want)
	}
}

func TestShuffledInputGivesIdenticalSnapshot(t *testing.T) {
	base := []Item{
		{"e", 3.0}, {"b", 1.0}, {"a", 1.0}, {"d", 2.0},
		{"c", 1.0}, {"f", 3.0}, {"g", -1.0}, {"h", 2.0},
	}
	for _, dir := range []Direction{Desc, Asc} {
		var want []Item
		rng := rand.New(rand.NewSource(42))
		for trial := 0; trial < 20; trial++ {
			perm := rng.Perm(len(base))
			s, err := New(4, dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range perm {
				s.Push(base[i].ID, base[i].Score)
			}
			got := s.Snapshot()
			if trial == 0 {
				want = got
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("dir=%v trial=%d: %v != %v", dir, trial, got, want)
			}
		}
	}
}
