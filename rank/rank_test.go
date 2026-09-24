package rank

import (
	"errors"
	"strconv"
	"testing"

	"ontology/ord"
)

func tr(s *Set, id string) ord.Triple { t, _ := s.Get(id); return t }

func all(s *Set) map[string]ord.Triple {
	m := map[string]ord.Triple{}
	for _, e := range s.Snapshot() {
		m[e.ID] = tr(s, e.ID)
	}
	return m
}

// TestSixStep replays NOTES.md's six operations and checks every element's
// triple after each step (the required step-by-step derivation).
func TestSixStep(t *testing.T) {
	steps := []struct {
		op   func(*Set) error
		want map[string]ord.Triple
	}{
		{func(s *Set) error { return s.Add("A", 100) },
			map[string]ord.Triple{"A": {RowNumber: 1, Rank: 1, DenseRank: 1}}},
		{func(s *Set) error { return s.Add("B", 90) },
			map[string]ord.Triple{"A": {RowNumber: 1, Rank: 1, DenseRank: 1}, "B": {RowNumber: 2, Rank: 2, DenseRank: 2}}},
		{func(s *Set) error { return s.Add("C", 100) },
			map[string]ord.Triple{"A": {RowNumber: 1, Rank: 1, DenseRank: 1}, "C": {RowNumber: 2, Rank: 1, DenseRank: 1}, "B": {RowNumber: 3, Rank: 3, DenseRank: 2}}},
		{func(s *Set) error { return s.Add("D", 80) },
			map[string]ord.Triple{"A": {RowNumber: 1, Rank: 1, DenseRank: 1},
				"C": {RowNumber: 2, Rank: 1, DenseRank: 1}, "B": {RowNumber: 3, Rank: 3, DenseRank: 2},
				"D": {RowNumber: 4, Rank: 4, DenseRank: 3}}},
		{func(s *Set) error { return s.Add("E", 90) },
			map[string]ord.Triple{"A": {RowNumber: 1, Rank: 1, DenseRank: 1},
				"C": {RowNumber: 2, Rank: 1, DenseRank: 1}, "B": {RowNumber: 3, Rank: 3, DenseRank: 2},
				"E": {RowNumber: 4, Rank: 3, DenseRank: 2}, "D": {RowNumber: 5, Rank: 5, DenseRank: 3}}},
		{func(s *Set) error { return s.Remove("C") },
			map[string]ord.Triple{"A": {RowNumber: 1, Rank: 1, DenseRank: 1},
				"B": {RowNumber: 2, Rank: 2, DenseRank: 2}, "E": {RowNumber: 3, Rank: 2, DenseRank: 2},
				"D": {RowNumber: 4, Rank: 4, DenseRank: 3}}},
	}
	s := New()
	for i, st := range steps {
		if err := st.op(s); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		got := all(s)
		if len(got) != len(st.want) {
			t.Fatalf("step %d: %d elements, want %d", i+1, len(got), len(st.want))
		}
		for id, w := range st.want {
			if got[id] != w {
				t.Fatalf("step %d: %s = %+v, want %+v", i+1, id, got[id], w)
			}
		}
	}
}

// TestComplexityRewriteBound is the mandated proof: after m distinct inserts,
// inserting a new top score must rewrite a number of stored rank fields that
// stays at an m-independent constant (the counter is read directly because
// this is an in-package test; no exported API ever exposes its value).
func TestComplexityRewriteBound(t *testing.T) {
	const bound = 3
	cases := []int{100, 1000, 10000}
	first := -1
	for _, m := range cases {
		s := New()
		for i := 0; i < m; i++ {
			if err := s.Add(strconv.Itoa(i), int64(i)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add("top", int64(m)); err != nil {
			t.Fatal(err)
		}
		if s.lastRewrite > bound {
			t.Fatalf("m=%d rewrote %d fields, bound %d", m, s.lastRewrite, bound)
		}
		if first == -1 {
			first = s.lastRewrite
		} else if s.lastRewrite != first {
			t.Fatalf("rewrite count grew with m: %d then %d", first, s.lastRewrite)
		}
	}
}

// TestSentinelsDistinct checks the three rejection errors are decidable and
// pairwise different.
func TestSentinelsDistinct(t *testing.T) {
	s := New()
	cases := []struct {
		err  error
		want error
	}{
		{s.Add("", 1), ErrEmptyID},
		{s.Add("A", 1), nil},
		{s.Add("A", 2), ErrDuplicateID},
		{s.Remove("x"), ErrIDNotFound},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("case %d: got %v, want %v", i, c.err, c.want)
		}
	}
	if ErrEmptyID == ErrDuplicateID || ErrEmptyID == ErrIDNotFound || ErrDuplicateID == ErrIDNotFound {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}
