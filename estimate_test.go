package ontology

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestEstimateEmptyIsZero(t *testing.T) {
	h, _ := New(16)
	if got := h.Estimate(); got != 0 {
		t.Fatalf("empty estimator: got %d, want 0", got)
	}
}

// TestLinearCountingExact adds hashes one at a time and requires the
// estimate to equal the true cardinality exactly for every n in
// 1..100 (the linear-counting range).
func TestLinearCountingExact(t *testing.T) {
	h, _ := New(16)
	for i, hash := range distinctHashes(1, 100) {
		h.Add(hash)
		if got, want := h.Estimate(), uint64(i+1); got != want {
			t.Fatalf("n=%d: estimate=%d, want exact %d", i+1, got, want)
		}
	}
}

// TestLargeCardinalityError checks the relative error stays under 15%
// at 100, 1_000 and 100_000 distinct hashes.
func TestLargeCardinalityError(t *testing.T) {
	for _, n := range []int{100, 1_000, 100_000} {
		h, _ := New(14)
		for _, hash := range distinctHashes(uint64(n), n) {
			h.Add(hash)
		}
		est := h.Estimate()
		diff := float64(est) - float64(n)
		if diff < 0 {
			diff = -diff
		}
		if rel := diff / float64(n); rel >= 0.15 {
			t.Fatalf("n=%d: estimate=%d, relative error %.4f >= 0.15", n, est, rel)
		}
	}
}

// TestShuffledInsertOrderSameSnapshot inserts the same hashes in two
// different orders and requires byte-identical register snapshots.
func TestShuffledInsertOrderSameSnapshot(t *testing.T) {
	hashes := distinctHashes(7, 10_000)
	a, _ := New(12)
	for _, hash := range hashes {
		a.Add(hash)
	}
	shuffled := make([]uint64, len(hashes))
	copy(shuffled, hashes)
	rand.New(rand.NewSource(99)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	b, _ := New(12)
	for _, hash := range shuffled {
		b.Add(hash)
	}
	if !bytes.Equal(a.InspectRegisters(), b.InspectRegisters()) {
		t.Fatal("register snapshots differ after shuffled insertion")
	}
}

// TestAddIdempotent adds the same hash a million times and requires
// the snapshot and estimate to stay frozen.
func TestAddIdempotent(t *testing.T) {
	h, _ := New(10)
	const hash = 0xDEADBEEFCAFEF00D
	h.Add(hash)
	before := h.InspectRegisters()
	estBefore := h.Estimate()
	for i := 0; i < 1_000_000; i++ {
		h.Add(hash)
	}
	if !bytes.Equal(before, h.InspectRegisters()) {
		t.Fatal("snapshot changed after 1e6 duplicate adds")
	}
	if got := h.Estimate(); got != estBefore {
		t.Fatalf("estimate changed: before=%d after=%d", estBefore, got)
	}
}
