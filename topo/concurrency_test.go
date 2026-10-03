package topo

import (
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentAccess hammers one Maintainer from many goroutines; the
// result must be race-free and leave a valid topological order, i.e. the
// concurrent execution is equivalent to some serial order and Order never
// observes a half-finished reordering.
func TestConcurrentAccess(t *testing.T) {
	m := New(300, 5000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 400; i++ {
				switch rng.Intn(6) {
				case 0:
					m.AddNode()
				case 1:
					m.AddEdge(rng.Intn(300), rng.Intn(300))
				case 2:
					m.RemoveEdge(rng.Intn(300), rng.Intn(300))
				case 3:
					m.RemoveNode(rng.Intn(300))
				case 4:
					b := make([][2]int, 1+rng.Intn(4))
					for j := range b {
						b[j] = [2]int{rng.Intn(300), rng.Intn(300)}
					}
					m.AddEdges(b)
				case 5:
					// Atomic spot check under the lock: Order must be a
					// consistent snapshot, never a half-finished reorder.
					m.mu.Lock()
					ord := m.orderLocked()
					prev := -1
					for _, x := range ord {
						o := m.ord[x]
						if o <= prev {
							t.Errorf("Order not sorted by ord: %d after %d", o, prev)
						}
						prev = o
					}
					for u, ws := range m.out {
						for w := range ws {
							if m.ord[u] >= m.ord[w] {
								t.Errorf("edge %d->%d violates order", u, w)
							}
						}
					}
					m.mu.Unlock()
				}
			}
		}(int64(g)*131 + 7)
	}
	wg.Wait()

	// Final state must satisfy every invariant.
	seenOrd := map[int]int{}
	for _, x := range m.Order() {
		o, err := m.OrdOf(x)
		if err != nil {
			t.Fatalf("node %d in Order but OrdOf failed: %v", x, err)
		}
		if prev, dup := seenOrd[o]; dup {
			t.Fatalf("order value %d shared by %d and %d", o, prev, x)
		}
		seenOrd[o] = x
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	edgeCnt := 0
	for u, ws := range m.out {
		for w := range ws {
			edgeCnt++
			if !m.in[w][u] {
				t.Fatalf("edge %d->%d missing from in-adjacency", u, w)
			}
			if m.ord[u] >= m.ord[w] {
				t.Fatalf("edge %d->%d violates order %d !< %d", u, w, m.ord[u], m.ord[w])
			}
		}
	}
	if edgeCnt != m.edgeCount {
		t.Fatalf("edgeCount = %d, actual %d", m.edgeCount, edgeCnt)
	}
}
