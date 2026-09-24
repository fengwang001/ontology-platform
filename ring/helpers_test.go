package ring

import (
	"fmt"
	"testing"
)

const (
	testKeyCount   = 100000
	testNodeCount  = 10
	testVnodes     = 200
	testVNodeRatio = 1
)

// genKeys deterministically generates n distinct keys.
func genKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("key-%08d", i)
	}
	return keys
}

func nodeID(i int) string {
	return fmt.Sprintf("node-%02d", i)
}

// buildRing creates a ring with nodes node-00 .. node-(n-1), each with
// the given number of vnodes.
func buildRing(tb testing.TB, nodes, vnodes int) *Ring {
	tb.Helper()
	r := New()
	for i := 0; i < nodes; i++ {
		if err := r.Add(nodeID(i), vnodes); err != nil {
			tb.Fatalf("Add(%s): %v", nodeID(i), err)
		}
	}
	return r
}

// locateAll maps every key to its owner, failing on any error.
func locateAll(tb testing.TB, r *Ring, keys []string) []string {
	tb.Helper()
	owners := make([]string, len(keys))
	for i, k := range keys {
		owner, err := r.Locate(k)
		if err != nil {
			tb.Fatalf("Locate(%q): %v", k, err)
		}
		if owner == "" {
			tb.Fatalf("Locate(%q) returned empty node", k)
		}
		owners[i] = owner
	}
	return owners
}

// distribution counts how many of the keys each node owns.
func distribution(tb testing.TB, r *Ring, keys []string) map[string]int {
	tb.Helper()
	counts := make(map[string]int)
	for _, owner := range locateAll(tb, r, keys) {
		counts[owner]++
	}
	return counts
}

// maxMinRatio returns max/min over the values; +Inf if min is 0.
func maxMinRatio(counts map[string]int) float64 {
	min, max := 0, 0
	first := true
	for _, c := range counts {
		if first || c < min {
			min = c
		}
		if first || c > max {
			max = c
		}
		first = false
	}
	if min == 0 {
		if max == 0 {
			return 1
		}
		return maxFloat()
	}
	return float64(max) / float64(min)
}

func maxFloat() float64 {
	return 1.7976931348623157e308
}
