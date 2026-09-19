package ontology

import (
	"math"
	"reflect"
	"testing"
)

func TestSignedZeroTie(t *testing.T) {
	for _, dir := range []Direction{Desc, Asc} {
		s, _ := New(3, dir)
		s.Push("p", math.Float64frombits(0x8000000000000000)) // -0.0
		s.Push("q", 0)
		s.Push("r", math.Float64frombits(0)) // +0.0

		got := ids(s.Snapshot())
		want := []string{"p", "q", "r"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("dir=%v got %v, want %v", dir, got, want)
		}
	}
}

func TestInfinitiesRanked(t *testing.T) {
	s, _ := New(3, Desc)
	s.Push("lo", -math.Inf(1))
	s.Push("hi", math.Inf(1))
	s.Push("mid", 0)
	s.Push("neg", -42)

	got := ids(s.Snapshot())
	want := []string{"hi", "mid", "neg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("desc infinities = %v, want %v", got, want)
	}

	a, _ := New(3, Asc)
	a.Push("lo", -math.Inf(1))
	a.Push("hi", math.Inf(1))
	a.Push("mid", 0)
	a.Push("pos", 42)
	got = ids(a.Snapshot())
	want = []string{"lo", "mid", "pos"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("asc infinities = %v, want %v", got, want)
	}
}

func TestNaNDoesNotOverwrite(t *testing.T) {
	s, _ := New(2, Desc)
	s.Push("a", 5)
	s.Push("a", math.NaN())
	got := s.Snapshot()
	if len(got) != 1 || got[0].ID != "a" || got[0].Score != 5 {
		t.Fatalf("NaN overwrite mutated state: %+v", got)
	}
	if s.Skipped() != 1 {
		t.Fatalf("skipped = %d", s.Skipped())
	}
}
