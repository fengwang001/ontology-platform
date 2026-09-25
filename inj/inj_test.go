package inj

import (
	"testing"

	"ontology/idx"
)

func buildJoiner(t *testing.T, keys []int) *Joiner {
	t.Helper()
	x, err := idx.Build(keys)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	j := NewJoiner()
	j.SetIndex(x)
	return j
}

// ceilLog2Plus1 is the binary-search locating bound: ceil(log2 m) + 1.
func ceilLog2Plus1(m int) int64 {
	bound := int64(1)
	for n := m; n > 1; n = (n + 1) / 2 {
		bound++
	}
	return bound
}

// TestProbeCostLogarithmic pins the non-exported cmps counter (white-box,
// same package): locating one key must cost <= ceil(log2 m)+1 comparisons
// of index entries, i.e. binary search, never a linear scan.
func TestProbeCostLogarithmic(t *testing.T) {
	for _, m := range []int{100, 317, 1000, 5000, 10000} {
		keys := make([]int, m)
		for i := range keys {
			keys[i] = i
		}
		j := buildJoiner(t, keys)
		if _, err := j.Probe([]int{m / 2}); err != nil {
			t.Fatalf("m=%d Probe: %v", m, err)
		}
		if got, bound := j.cmps.Load(), ceilLog2Plus1(m); got > bound {
			t.Errorf("m=%d: locating comparisons %d > bound %d (linear scan?)", m, got, bound)
		}
	}
}

// TestProbeIndependentPerKey pins the spec example: every tuple of R is
// probed independently, so the repeated key 3 matches 3 tuples both times.
func TestProbeIndependentPerKey(t *testing.T) {
	j := buildJoiner(t, []int{2, 3, 3, 3, 5, 7})
	got, err := j.Probe([]int{3, 5, 1, 3})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	want := []Pair{{3, 3}, {3, 3}, {3, 3}, {5, 5}, {3, 3}, {3, 3}, {3, 3}}
	if len(got) != len(want) {
		t.Fatalf("got %d pairs %v, want %d", len(got), got, len(want))
	}
	for i, p := range want {
		if got[i] != p {
			t.Errorf("pair %d: got %v, want %v", i, got[i], p)
		}
	}
}

// TestProbeRejectsLeaveCounterAlone: a rejected batch fails before any
// locating happens, so the counter keeps its previous value.
func TestProbeRejectsLeaveCounterAlone(t *testing.T) {
	j := buildJoiner(t, []int{1, 2, 3})
	if _, err := j.Probe([]int{2}); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	before := j.cmps.Load()
	for _, bad := range [][]int{nil, {-1}} {
		if _, err := j.Probe(bad); err == nil {
			t.Fatalf("Probe(%v) accepted", bad)
		}
	}
	if got := j.cmps.Load(); got != before {
		t.Errorf("counter moved by rejected probes: %d -> %d", before, got)
	}
}

func TestCheckCost(t *testing.T) {
	if err := CheckCost(); err != nil {
		t.Fatalf("CheckCost: %v", err)
	}
}
