package ontology

import (
	"fmt"
	"slices"
	"testing"
)

// streamOf generates a deterministic stream of n elements with the
// given weight.
func streamOf(n int, weight float64) []string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf("item-%06d", i)
	}
	return items
}

func feed(t *testing.T, r *Reservoir, items []string, weight float64) {
	t.Helper()
	for _, it := range items {
		if err := r.Add(it, weight); err != nil {
			t.Fatalf("Add(%q) failed: %v", it, err)
		}
	}
}

// Same seed + same input sequence must produce element-for-element
// identical samples (including order) and consume the same number of
// random numbers.
func TestSameSeedReproducible(t *testing.T) {
	items := streamOf(5000, 3)
	const k = 50
	const seed = 42

	r1, err := New(k, seed)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := New(k, seed)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, r1, items, 3)
	feed(t, r2, items, 3)

	s1, s2 := r1.Sample(), r2.Sample()
	if !slices.Equal(s1, s2) {
		t.Fatalf("same seed produced different samples:\n%v\n%v", s1, s2)
	}
	if r1.RandConsumed() != r2.RandConsumed() {
		t.Fatalf("rand consumed differs: %d vs %d",
			r1.RandConsumed(), r2.RandConsumed())
	}
	if r1.RandConsumed() != uint64(len(items)) {
		t.Fatalf("expected %d draws, got %d", len(items), r1.RandConsumed())
	}
}

// Different seeds on the same input must be able to produce different
// results: across a set of seeds, at least two samples differ.
func TestDifferentSeedsDiffer(t *testing.T) {
	items := streamOf(2000, 1)
	seen := map[string]bool{}
	for seed := uint64(1); seed <= 8; seed++ {
		r, err := New(30, seed)
		if err != nil {
			t.Fatal(err)
		}
		feed(t, r, items, 1)
		seen[fmt.Sprint(r.Sample())] = true
	}
	if len(seen) < 2 {
		t.Fatalf("all seeds produced identical samples")
	}
}

// Sample must be idempotent: without new Adds, repeated Sample calls
// return identical results. Randomness happens only at Add time.
func TestSampleIdempotent(t *testing.T) {
	r, err := New(20, 7)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, r, streamOf(1000, 2), 2)
	first := r.Sample()
	for i := 0; i < 5; i++ {
		if got := r.Sample(); !slices.Equal(first, got) {
			t.Fatalf("Sample call %d differs from first", i+2)
		}
	}
	if r.RandConsumed() != 1000 {
		t.Fatalf("Sample consumed randomness: %d draws", r.RandConsumed())
	}
}

// The slice returned by Sample must be isolated from internal state:
// mutating it must not affect subsequent Sample results.
func TestSampleIsolation(t *testing.T) {
	r, err := New(10, 9)
	if err != nil {
		t.Fatal(err)
	}
	feed(t, r, streamOf(500, 1), 1)
	before := r.Sample()
	mutated := r.Sample()
	for i := range mutated {
		mutated[i] = "corrupted"
	}
	after := r.Sample()
	if !slices.Equal(before, after) {
		t.Fatalf("mutating returned slice affected internal state")
	}
}
