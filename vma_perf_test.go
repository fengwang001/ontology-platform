package ontology

import (
	"flag"
	"math"
	"testing"
)

var slow = flag.Bool("slow", false, "run large structure counter tests")

func TestVisitedCounters(t *testing.T) {
	if !*slow {
		t.Skip("set -slow to run n=100000 counter test")
	}
	const n = 100_000
	high := int64(2*n + 2)
	m, err := New(1, high, n+10, 1, high)
	if err != nil {
		t.Fatal(err)
	}
	seed := make([]VMA, 0, n)
	for i := int64(0); i < n; i++ {
		start := 2 + 2*i
		seed = append(seed, VMA{Start: start, End: start + 1, Perm: int(i%2) + 1, Anonymous: true})
	}
	m.replaceAll(seed)
	findLimit := 2*int(math.Ceil(math.Log2(float64(n+2)))) + 3
	searchLimit := 4*int(math.Ceil(math.Log2(float64(n+2)))) + 8
	m.tree.visited = 0
	if _, ok := m.Find(n + 2); !ok {
		t.Fatal("find missed seeded VMA")
	}
	if int(m.tree.visited) > findLimit {
		t.Fatalf("Find visited=%d, limit=%d", m.tree.visited, findLimit)
	}
	m.blocked.visited = 0
	if _, ok := m.blocked.rightmostFree(1); !ok {
		t.Fatal("rightmost free missing")
	}
	if int(m.blocked.visited) > searchLimit {
		t.Fatalf("rightmostFree visited=%d, limit=%d", m.blocked.visited, searchLimit)
	}
}
