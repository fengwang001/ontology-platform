package gossip

import (
	"fmt"
	"math/rand"
	"testing"
)

func build(t *testing.T, m int) *Graph {
	t.Helper()
	g := New()
	for k := 0; k < m; k++ {
		if err := g.Add(fmt.Sprintf("n%05d", k), k*7-300); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

// TestExchangeTouchesTwoNodes proves Exchange only ever reads/writes the
// two participating nodes, independent of the total node count m: the
// unexported counter stays at 2 for every exchange at every scale.
func TestExchangeTouchesTwoNodes(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		g := build(t, m)
		for k := 0; k < 50; k++ {
			a, b := fmt.Sprintf("n%05d", k), fmt.Sprintf("n%05d", (k*37+1)%m)
			if a == b {
				continue
			}
			if err := g.Exchange(a, b); err != nil {
				t.Fatal(err)
			}
			if g.touched != 2 {
				t.Fatalf("m=%d: exchange touched %d nodes, want constant 2", m, g.touched)
			}
		}
	}
}

// naiveStep is an independent, map-based reference of the exchange rule.
func naiveStep(vals map[string]int, a, b string) {
	tot := vals[a] + vals[b]
	if tot%2 == 0 {
		vals[a], vals[b] = tot/2, tot/2
		return
	}
	lo, hi := (tot-1)/2, (tot+1)/2
	if a > b {
		lo, hi = hi, lo
	}
	vals[a], vals[b] = lo, hi
}

// TestSumAndSpreadInvariants pins invariants 1 (sum conserved) and 2
// (spread non-increasing) over random exchange sequences at several
// scales, and invariant 3 (matches a naive step-by-step reference).
func TestSumAndSpreadInvariants(t *testing.T) {
	for _, m := range []int{2, 3, 17, 100} {
		g := build(t, m)
		ref := map[string]int{}
		for k := 0; k < m; k++ {
			ref[fmt.Sprintf("n%05d", k)] = k*7 - 300
		}
		want := g.Sum()
		rng := rand.New(rand.NewSource(int64(m)))
		prev := g.Spread()
		for step := 0; step < 200; step++ {
			x, y := rng.Intn(m), rng.Intn(m)
			if x == y {
				continue
			}
			a, b := fmt.Sprintf("n%05d", x), fmt.Sprintf("n%05d", y)
			if err := g.Exchange(a, b); err != nil {
				t.Fatal(err)
			}
			naiveStep(ref, a, b)
			if got := g.Sum(); got != want {
				t.Fatalf("m=%d step=%d: sum=%d want %d", m, step, got, want)
			}
			if s := g.Spread(); s > prev {
				t.Fatalf("m=%d step=%d: spread %d > previous %d", m, step, s, prev)
			} else {
				prev = s
			}
		}
		for id, want := range ref {
			if got, err := g.Value(id); err != nil || got != want {
				t.Fatalf("m=%d: %s=%d want %d (err %v)", m, id, got, want, err)
			}
		}
	}
}
