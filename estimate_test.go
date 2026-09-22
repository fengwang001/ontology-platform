package hll

import (
	"math"
	"math/rand"
	"testing"
)

// splitmix64 is a deterministic, well-mixed 64-bit permixing step used to
// fabricate distinct, hash-like uint64 values in tests.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// distinctHashes returns n deterministically generated, guaranteed-distinct
// hashes (any astronomically unlikely splitmix collision is filtered out by
// the set).
func distinctHashes(n int) []uint64 {
	out := make([]uint64, 0, n)
	seen := make(map[uint64]struct{}, n)
	for x := uint64(0); len(out) < n; x++ {
		h := splitmix64(x)
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

// TestExactSmallCardinality checks 0,1,10,100 (and every count up to 100)
// are returned with zero error.
func TestExactSmallCardinality(t *testing.T) {
	hs := distinctHashes(100)
	for _, n := range []int{0, 1, 10, 100} {
		e, _ := New(10)
		for _, h := range hs[:n] {
			e.Add(h)
		}
		if got := e.Estimate(); int(got) != n {
			t.Fatalf("Estimate after %d adds = %d, want exact %d", n, got, n)
		}
	}

	// Belt and suspenders: every count from 0 to 100 must be exact, not
	// just the sampled points.
	e, _ := New(12)
	for n := 0; n <= 100; n++ {
		if n > 0 {
			e.Add(hs[n-1])
		}
		if got := e.Estimate(); int(got) != n {
			t.Fatalf("at distinct count %d Estimate = %d, want %d", n, got, n)
		}
	}
}

// TestLargeCardinalityRelativeError asserts the observed relative error for
// 100 / 1000 / 100000 distinct hashes stays below 15%.
func TestLargeCardinalityRelativeError(t *testing.T) {
	for _, n := range []int{100, 1000, 100000} {
		e, _ := New(14)
		for _, h := range distinctHashes(n) {
			e.Add(h)
		}
		got := float64(e.Estimate())
		rel := math.Abs(got-float64(n)) / float64(n)
		if rel >= 0.15 {
			t.Fatalf("n=%d estimate=%.0f relative error %.4f >= 0.15", n, got, rel)
		}
	}
}

// TestInsertionOrderIndependent inserts the same multiset in forward and in a
// deterministically shuffled order and requires byte-identical register
// snapshots (and equal estimates).
func TestInsertionOrderIndependent(t *testing.T) {
	hs := distinctHashes(5000)

	a, _ := New(12)
	for _, h := range hs {
		a.Add(h)
	}

	shuffled := append([]uint64(nil), hs...)
	r := rand.New(rand.NewSource(1))
	r.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	b, _ := New(12)
	for _, h := range shuffled {
		b.Add(h)
	}

	sa, sb := a.InspectRegisters(), b.InspectRegisters()
	if !regsEqual(sa, sb) {
		t.Fatalf("snapshots differ by insertion order:\na=%v\nb=%v", sa, sb)
	}
	if a.Estimate() != b.Estimate() {
		t.Fatalf("estimates differ: %d vs %d", a.Estimate(), b.Estimate())
	}
}
