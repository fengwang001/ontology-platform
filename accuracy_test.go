package hll

import (
	"math"
	"slices"
	"testing"
)

// Estimator used for exact small-cardinality checks: p=16 gives 65536
// registers, so up to 100 distinct hashes never collide and linear counting is
// exact.
const exactP = 16

func TestEstimateZero(t *testing.T) {
	e, _ := New(exactP)
	if got := e.Estimate(); got != 0 {
		t.Fatalf("empty Estimate = %d, want 0", got)
	}
}

// TestLinearCountingExact requires Estimate to equal the true cardinality for
// every cardinality from 1 through 100, not merely for sampled sizes.
func TestLinearCountingExact(t *testing.T) {
	hashes := distinctHashes(100)
	for n := 1; n <= 100; n++ {
		e, _ := New(exactP)
		for _, h := range hashes[:n] {
			e.Add(h)
		}
		if got := e.Estimate(); got != uint64(n) {
			t.Fatalf("n=%d Estimate = %d, want exactly %d", n, got, n)
		}
	}
}

func TestEstimateOneAndTen(t *testing.T) {
	for _, n := range []int{1, 10} {
		e, _ := New(exactP)
		for _, h := range distinctHashes(n) {
			e.Add(h)
		}
		if got := e.Estimate(); got != uint64(n) {
			t.Fatalf("n=%d Estimate = %d, want %d", n, got, n)
		}
	}
}

// within15 reports |est-truth|/truth < 0.15.
func within15(est, truth uint64) bool {
	rel := math.Abs(float64(est)-float64(truth)) / float64(truth)
	return rel < 0.15
}

// TestLargeCardinality checks relative error below 15% at 100, 1000 and
// 100000 distinct, deterministically generated hashes.
func TestLargeCardinality(t *testing.T) {
	const p = 10
	for _, n := range []int{100, 1000, 100000} {
		e, _ := New(p)
		for _, h := range distinctHashes(n) {
			e.Add(h)
		}
		got := e.Estimate()
		if !within15(got, uint64(n)) {
			t.Fatalf("n=%d Estimate = %d, rel error >= 15%%", n, got)
		}
		t.Logf("n=%d estimate=%d relErr=%.4f", n, got,
			math.Abs(float64(got)-float64(n))/float64(n))
	}
}

// TestInsertOrderIndependent asserts that shuffled insertion order produces a
// byte-for-byte identical register snapshot.
func TestInsertOrderIndependent(t *testing.T) {
	const p = 10
	hashes := distinctHashes(10000)

	a, _ := New(p)
	for _, h := range hashes {
		a.Add(h)
	}
	shuffled := shuffledCopy(hashes, 42)
	b, _ := New(p)
	for _, h := range shuffled {
		b.Add(h)
	}
	if !slices.Equal(a.InspectRegisters(), b.InspectRegisters()) {
		t.Fatal("register snapshots differ across insertion orders")
	}
}

// TestIdempotent verifies that adding one hash a million times never changes
// the estimate.
func TestIdempotent(t *testing.T) {
	e, _ := New(12)
	h := distinctHashes(1)[0]
	e.Add(h)
	before := e.InspectRegisters()
	est := e.Estimate()
	for i := 0; i < 1_000_000; i++ {
		e.Add(h)
	}
	after := e.InspectRegisters()
	if !slices.Equal(before, after) {
		t.Fatal("registers changed while re-adding the same hash")
	}
	if got := e.Estimate(); got != est {
		t.Fatalf("estimate changed after repeats: %d vs %d", got, est)
	}
}
