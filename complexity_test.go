package ontology

import (
	"fmt"
	"testing"
)

func chainID(i int) string { return fmt.Sprintf("v%05d", i) }

// TestChainFindHops builds the chain v0-v1-...-v20000 via sequential
// unions, then hammers the last element with Finds. With path
// compression the follow-up Finds are nearly free; without it the
// average hop count would stay in the thousands.
func TestChainFindHops(t *testing.T) {
	const n = 20_000
	var ds DisjointSet
	for i := 0; i < n; i++ {
		ds.Union(chainID(i), chainID(i+1))
	}
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount = %d, want 1", got)
	}

	rep, firstHops, err := ds.FindWithHops(chainID(n))
	if err != nil {
		t.Fatalf("FindWithHops: %v", err)
	}
	if rep != chainID(0) {
		t.Fatalf("representative = %q, want %q", rep, chainID(0))
	}
	t.Logf("first Find of %s: %d hops", chainID(n), firstHops)

	const lookups = 10_000
	total := 0
	for i := 0; i < lookups; i++ {
		_, hops, err := ds.FindWithHops(chainID(n))
		if err != nil {
			t.Fatalf("FindWithHops: %v", err)
		}
		total += hops
	}
	avg := float64(total) / lookups
	t.Logf("average hops over %d follow-up Finds: %.3f", lookups, avg)
	if avg >= 3 {
		t.Fatalf("average follow-up hops = %.3f, want < 3 (path compression missing?)", avg)
	}
}

// TestPathCompressionFlattensDeepTree builds a tree of depth ~14 via
// balanced doubling unions (union-by-rank alone cannot avoid this
// depth), then shows that one Find collapses the path to a single hop.
func TestPathCompressionFlattensDeepTree(t *testing.T) {
	const n = 16_384 // 2^14
	var ds DisjointSet
	for step := 1; step < n; step *= 2 {
		for i := 0; i+step < n; i += 2 * step {
			ds.Union(chainID(i), chainID(i+step))
		}
	}
	if got := ds.ClassCount(); got != 1 {
		t.Fatalf("ClassCount = %d, want 1", got)
	}

	// In the binomial tree built above, v[n-1] sits at depth log2(n)=14.
	deepest := chainID(n - 1)
	_, firstHops, err := ds.FindWithHops(deepest)
	if err != nil {
		t.Fatalf("FindWithHops: %v", err)
	}
	if firstHops < 10 {
		t.Fatalf("first Find hops = %d, want >= 10 (test setup should be deep)", firstHops)
	}

	_, secondHops, err := ds.FindWithHops(deepest)
	if err != nil {
		t.Fatalf("FindWithHops: %v", err)
	}
	if secondHops != 1 {
		t.Fatalf("second Find hops = %d, want 1 after path compression", secondHops)
	}

	// Every element must still report the same (smallest) representative.
	for _, id := range []string{chainID(0), deepest, chainID(n / 2)} {
		rep, err := ds.Find(id)
		if err != nil {
			t.Fatalf("Find(%q): %v", id, err)
		}
		if rep != chainID(0) {
			t.Fatalf("Find(%q) = %q, want %q", id, rep, chainID(0))
		}
	}
}
