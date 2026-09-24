package topk

import (
	"slices"
	"sort"
	"testing"

	"ontology/bound"
	"ontology/sketch"
)

func TestHeavyHittersNoFalseNegatives(t *testing.T) {
	cases := []struct {
		threshold uint64
		counts    map[string]uint64
	}{
		{5, map[string]uint64{"hot": 9, "edge": 5, "cold": 1}},
		{1, map[string]uint64{"a": 1, "b": 2, "c": 3}},
		{10, map[string]uint64{"x": 20, "y": 3, "z": 11}},
	}
	for _, tc := range cases {
		s, _ := sketch.New(8, 4, 0)
		tk := New(s, tc.threshold)
		for k, n := range tc.counts {
			if err := s.Add(k, n); err != nil {
				t.Fatal(err)
			}
			tk.Add(k)
		}
		got := tk.HeavyHitters()
		if !sort.StringsAreSorted(got) {
			t.Errorf("T=%d: result %v not sorted (order must be deterministic)", tc.threshold, got)
		}
		for k, n := range tc.counts {
			if n >= tc.threshold && !slices.Contains(got, k) {
				t.Errorf("T=%d: missed %q with true count %d", tc.threshold, k, n)
			}
		}
		for _, k := range got {
			if est, _ := s.Estimate(k); est < tc.threshold {
				t.Errorf("T=%d: reported %q with estimate %d", tc.threshold, k, est)
			}
		}
	}
}

func TestQueriesProportionalToCandidates(t *testing.T) {
	cands := []string{"a", "b", "c", "d", "e"}
	for _, w := range []int{1000, 100000} {
		s, _ := sketch.New(w, 4, 0)
		tk := New(s, 1)
		for _, k := range cands {
			if err := s.Add(k, 1); err != nil {
				t.Fatal(err)
			}
			tk.Add(k)
		}
		_ = tk.HeavyHitters()
		if got := tk.queries.Load(); got != int64(len(cands)) {
			t.Errorf("w=%d: HeavyHitters made %d estimates, want %d (candidates), not ~w*d=%d",
				w, got, len(cands), w*4)
		}
	}
}

func TestCertainAndVerify(t *testing.T) {
	cases := []struct {
		counts map[string]uint64
	}{
		{map[string]uint64{"a": 10, "b": 4, "c": 1}},
		{map[string]uint64{"only": 7}},
	}
	for _, tc := range cases {
		s, _ := sketch.New(16, 4, 0)
		tk := New(s, 3)
		for k, n := range tc.counts {
			if err := s.Add(k, n); err != nil {
				t.Fatal(err)
			}
			tk.Add(k)
		}
		hh, certain := tk.HeavyHitters(), tk.Certain()
		for _, k := range certain {
			if !slices.Contains(hh, k) {
				t.Errorf("Certain reports %q not in HeavyHitters", k)
			}
		}
		for k, n := range tc.counts {
			ok, err := bound.Verify(tk.s, k, n)
			if err != nil || !ok {
				t.Errorf("Verify(%q, %d) = %v, %v; want true, nil", k, n, ok, err)
			}
		}
	}
}
