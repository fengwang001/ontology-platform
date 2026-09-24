package ontology

import (
	"math"
	"strconv"
	"testing"
)

// nodeIDs returns n node IDs: "node-0" .. "node-n-1".
func nodeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = "node-" + strconv.Itoa(i)
	}
	return ids
}

// buildRing builds a ring from ids, each with vn vnodes, in the given
// order. It fails the test on any Add error.
func buildRing(t *testing.T, ids []string, vn int) *Ring {
	t.Helper()
	r := New()
	for _, id := range ids {
		if err := r.Add(id, vn); err != nil {
			t.Fatalf("Add(%q, %d): %v", id, vn, err)
		}
	}
	return r
}

// owners returns the owner of every key, in order. It fails the test if
// any Locate call errors.
func owners(t *testing.T, r *Ring, keys []string) []string {
	t.Helper()
	out := make([]string, len(keys))
	for i, k := range keys {
		o, err := r.Locate(k)
		if err != nil {
			t.Fatalf("Locate(%q): %v", k, err)
		}
		out[i] = o
	}
	return out
}

// distribution counts how many of the n deterministic keys each node owns.
func distribution(t *testing.T, ids []string, vn, nkeys int) map[string]int {
	t.Helper()
	r := buildRing(t, ids, vn)
	counts := make(map[string]int, len(ids))
	for _, o := range owners(t, r, Keys(nkeys)) {
		counts[o]++
	}
	return counts
}

// maxMinRatio returns max(count)/min(count) over the given ids. A node
// with zero keys yields +Inf.
func maxMinRatio(counts map[string]int, ids []string) float64 {
	hi, lo := 0, math.MaxInt
	for _, id := range ids {
		c := counts[id]
		if c > hi {
			hi = c
		}
		if c < lo {
			lo = c
		}
	}
	if lo == 0 {
		return math.Inf(1)
	}
	return float64(hi) / float64(lo)
}
