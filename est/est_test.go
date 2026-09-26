package est

import (
	"math"
	"testing"
)

// TestEstimateVisitsExactlyM proves Estimate reads m registers, not N elements.
func TestEstimateVisitsExactlyM(t *testing.T) {
	cases := []struct {
		m int
		n int
	}{
		{8, 100}, {8, 1000}, {8, 10000},
		{16, 100}, {16, 1000}, {16, 10000},
		{1024, 100}, {1024, 10000},
	}
	for _, c := range cases {
		e := New(c.m)
		for i := 0; i < c.n; i++ {
			e.Add(i%c.m, i/c.m)
		}
		e.Estimate()
		if e.visited != c.m {
			t.Fatalf("m=%d n=%d: visited %d registers, want %d", c.m, c.n, e.visited, c.m)
		}
	}
}

// TestEstimateFormula pins Estimate to α_m·m·2^mean on a known sequence.
func TestEstimateFormula(t *testing.T) {
	e := New(8)
	seq := [][2]int{{0, 0}, {2, 2}, {2, 4}, {5, 1}, {0, 3}, {5, 5}, {2, 3}}
	for _, s := range seq {
		e.Add(s[0], s[1])
	}
	want := Alpha(8) * 8 * math.Exp2(15.0/8.0)
	if got := e.Estimate(); got != want {
		t.Fatalf("Estimate()=%v, want %v", got, want)
	}
}

// TestAlphaTable pins a few table entries so the lookup cannot drift.
func TestAlphaTable(t *testing.T) {
	if Alpha(1) != 0.39701 {
		t.Fatalf("Alpha(1)=%v", Alpha(1))
	}
	for _, m := range []int{2, 4, 8, 16, 1024} {
		a := Alpha(m)
		if a <= 0 || math.IsNaN(a) || math.IsInf(a, 0) {
			t.Fatalf("Alpha(%d)=%v not a positive finite constant", m, a)
		}
	}
}
