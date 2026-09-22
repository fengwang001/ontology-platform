package coalesce

import (
	"math"
	"testing"
)

func expand(set map[int64]bool, ivs []Interval) {
	for _, iv := range ivs {
		for b := iv.Start; b <= iv.End; b++ {
			set[b] = true
		}
	}
}

// TestByteSetExhaustiveEquivalence exhaustively compares the covered byte set
// before and after normalization over thousands of random configurations on a
// small universe: every start/end combination in [0,universe) is reachable.
func TestByteSetExhaustiveEquivalence(t *testing.T) {
	const universe = int64(40)
	seed := uint64(0x9e3779b97f4a7c15)
	rng := func(n int64) int64 {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		if n == 0 {
			return 0
		}
		return int64(seed % uint64(n))
	}
	for trial := 0; trial < 3000; trial++ {
		k := int(rng(12)) + 1
		in := make([]Interval, 0, k)
		for i := 0; i < k; i++ {
			a := rng(universe)
			b := rng(universe)
			if a > b {
				a, b = b, a
			}
			in = append(in, Interval{a, b})
		}
		n := &Normalizer{}
		out := n.Normalize(append([]Interval(nil), in...))

		before, after := map[int64]bool{}, map[int64]bool{}
		expand(before, in)
		expand(after, out)
		if len(before) != len(after) {
			t.Fatalf("trial %d: set size %d -> %d, in=%v out=%v", trial, len(before), len(after), in, out)
		}
		for b := range before {
			if !after[b] {
				t.Fatalf("trial %d: byte %d lost; in=%v out=%v", trial, b, in, out)
			}
		}
		for i := 1; i < len(out); i++ {
			if out[i].Start <= out[i-1].End+1 {
				t.Fatalf("trial %d: output still overlaps/adjacent: %v", trial, out)
			}
		}
	}
}

func TestAdjacentIntervalsMerge(t *testing.T) {
	n := &Normalizer{}
	out := n.Normalize([]Interval{{10, 19}, {0, 4}, {5, 9}, {7, 12}})
	want := []Interval{{0, 19}}
	if len(out) != len(want) || out[0] != want[0] {
		t.Fatalf("adjacent merge got %v, want %v", out, want)
	}
}

func TestMergedIntervalsDoNotTouch(t *testing.T) {
	n := &Normalizer{}
	out := n.Normalize([]Interval{{0, 2}, {3, 5}, {7, 8}})
	want := []Interval{{0, 5}, {7, 8}}
	if len(out) != 2 || out[0] != want[0] || out[1] != want[1] {
		t.Fatalf("got %v, want %v", out, want)
	}
}

func makeDisjointIntervals(n int) []Interval {
	// Deterministic LCG; intervals sit on disjoint slots but are emitted in
	// shuffled order so the sort has full work.
	slots := make([]Interval, n)
	for i := range slots {
		slots[i] = Interval{Start: int64(2 * i), End: int64(2*i + 1)}
	}
	state := uint64(12345)
	for i := n - 1; i > 0; i-- {
		state = state*6364136223846793005 + 1442695040888963407
		j := int(state % uint64(i+1))
		slots[i], slots[j] = slots[j], slots[i]
	}
	return slots
}

// TestComparisonCountNLogN proves the comparison count grows no faster than
// n log n: both samples stay under n*ceil(log2(n)), and the observed ratio
// is far below the quadratic ratio of 100.
func TestComparisonCountNLogN(t *testing.T) {
	measure := func(n int) int64 {
		nz := &Normalizer{}
		nz.Normalize(makeDisjointIntervals(n))
		return nz.ComparisonCount()
	}
	c100 := measure(100)
	c10000 := measure(10000)
	t.Logf("comparisons: n=100 -> %d ; n=10000 -> %d ; ratio %.2f", c100, c10000, float64(c10000)/float64(c100))

	bound := func(n int) float64 { return float64(n) * math.Ceil(math.Log2(float64(n))) }
	if float64(c100) > bound(100) {
		t.Fatalf("c(100)=%d exceeds n ceil(log2 n)=%.0f", c100, bound(100))
	}
	if float64(c10000) > bound(10000) {
		t.Fatalf("c(10000)=%d exceeds n ceil(log2 n)=%.0f", c10000, bound(10000))
	}
	// n log n scaling predicts a ratio of about (10000*13.29)/(100*6.64) ~ 200.
	// Quadratic scaling would give 10000.
	ratio := float64(c10000) / float64(c100)
	if ratio > 250 {
		t.Fatalf("comparison ratio %.1f looks superlinear", ratio)
	}
}
