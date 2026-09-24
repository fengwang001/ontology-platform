package ontology

import (
	"strconv"
	"testing"
)

func nodeID(i int) string {
	return "node-" + strconv.Itoa(i)
}

func buildRing(t *testing.T, nodes, vnodes int) *Ring {
	t.Helper()
	r := New()
	for i := 0; i < nodes; i++ {
		if err := r.Add(nodeID(i), vnodes); err != nil {
			t.Fatalf("Add(%s): %v", nodeID(i), err)
		}
	}
	return r
}

func ownersOf(t *testing.T, r *Ring, keys []string) []string {
	t.Helper()
	owners := make([]string, len(keys))
	for i, k := range keys {
		owner, err := r.Locate(k)
		if err != nil {
			t.Fatalf("Locate(%q): %v", k, err)
		}
		if owner == "" {
			t.Fatalf("Locate(%q) returned empty owner", k)
		}
		owners[i] = owner
	}
	return owners
}

func distributionOf(t *testing.T, r *Ring, keys []string) map[string]int {
	t.Helper()
	dist := make(map[string]int)
	for _, owner := range ownersOf(t, r, keys) {
		dist[owner]++
	}
	return dist
}

func maxMinRatio(dist map[string]int) float64 {
	max, min := 0, -1
	for _, c := range dist {
		if c > max {
			max = c
		}
		if min < 0 || c < min {
			min = c
		}
	}
	if min == 0 {
		return 0
	}
	return float64(max) / float64(min)
}
