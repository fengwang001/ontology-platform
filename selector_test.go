package ontology

import (
	"math"
	"reflect"
	"testing"
)

func ids(es []Element) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func TestDescRankingAndTieBreak(t *testing.T) {
	s, err := New(3, Desc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Push("a", 1)
	s.Push("b", 3)
	s.Push("c", 2)
	s.Push("d", 3) // ties with b; smaller ID wins

	got := ids(s.Snapshot())
	want := []string{"b", "d", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

func TestAscRankingAndTieBreak(t *testing.T) {
	s, _ := New(3, Asc)
	s.Push("a", 3)
	s.Push("b", 1)
	s.Push("c", 2)
	s.Push("d", 1)

	got := ids(s.Snapshot())
	want := []string{"b", "d", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
}

func TestTieAcrossBoundary(t *testing.T) {
	// K=2, scores: three items tied at 1 plus one item at 0/2 depending dir.
	// The two with lexicographically smaller IDs must survive regardless of
	// arrival order.
	orders := [][]string{
		{"c", "b", "a", "x"},
		{"x", "a", "b", "c"},
		{"b", "x", "c", "a"},
		{"a", "c", "x", "b"},
	}
	for _, dir := range []Direction{Desc, Asc} {
		outerScore := 0.0
		if dir == Desc {
			outerScore = -1
		} else {
			outerScore = 5
		}
		var prev []string
		for oi, order := range orders {
			s, _ := New(2, dir)
			for _, id := range order {
				sc := 1.0
				if id == "x" {
					sc = outerScore
				}
				s.Push(id, sc)
			}
			got := ids(s.Snapshot())
			want := []string{"a", "b"}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("dir=%v order#%d = %v, want %v", dir, oi, got, want)
			}
			if prev != nil && !reflect.DeepEqual(prev, got) {
				t.Fatalf("dir=%v order-dependent: %v vs %v", dir, prev, got)
			}
			prev = got
		}
	}
}

func TestOrderIndependenceShuffled(t *testing.T) {
	base := []Element{
		{"m", 5}, {"a", 1}, {"z", 1}, {"b", 4}, {"q", 4},
		{"n", 5}, {"c", 0}, {"r", 4}, {"d", 2},
	}
	perms := [][]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8},
		{8, 7, 6, 5, 4, 3, 2, 1, 0},
		{2, 0, 8, 1, 7, 3, 6, 4, 5},
		{6, 4, 2, 8, 0, 5, 3, 1, 7},
	}
	for _, dir := range []Direction{Desc, Asc} {
		var ref []Element
		for _, p := range perms {
			s, _ := New(4, dir)
			for _, i := range p {
				e := base[i]
				s.Push(e.ID, e.Score)
			}
			got := s.Snapshot()
			if ref == nil {
				ref = got
				continue
			}
			if !reflect.DeepEqual(got, ref) {
				t.Fatalf("dir=%v perm mismatch: %v vs %v", dir, got, ref)
			}
		}
	}
}

func TestNaNRejected(t *testing.T) {
	s, _ := New(2, Desc)
	s.Push("bad", math.NaN())
	s.Push("ok", 1)
	s.Push("bad2", math.NaN())

	if got := s.Skipped(); got != 2 {
		t.Fatalf("skipped = %d, want 2", got)
	}
	if got := ids(s.Snapshot()); !reflect.DeepEqual(got, []string{"ok"}) {
		t.Fatalf("snapshot = %v", got)
	}
	if s.Len() != 1 {
		t.Fatalf("len = %d, want 1", s.Len())
	}
}
