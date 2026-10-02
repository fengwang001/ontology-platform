package ontology

import (
	"sync"
	"testing"
)

func TestLongChainLocalTouched(t *testing.T) {
	const n = 100000
	m := NewMaintainer(n, n)
	for i := 0; i < n; i++ {
		if _, err := m.AddNode(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.AddEdge(0, 2); err != nil {
		t.Fatal(err)
	}
	for u := 2; u < n-1; u++ {
		if _, err := m.AddEdge(u, u+1); err != nil {
			t.Fatalf("AddEdge(%d,%d): %v", u, u+1, err)
		}
	}

	result, err := m.AddEdge(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Moved) != 2 || result.Forward != 1 || result.Backward != 1 {
		t.Fatalf("result = %+v, want exactly two touched nodes", result)
	}
	if m.Touched() != 2 {
		t.Fatalf("Touched = %d, want 2", m.Touched())
	}
	assertTopology(t, m, 0, -1)
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	m := NewMaintainer(40, 500)
	for i := 0; i < 20; i++ {
		if _, err := m.AddNode(); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				u := (i*7 + worker) % 18
				v := (u + 1 + (i+worker)%3) % 20
				if _, err := m.AddEdge(u, v); err != nil {
					_ = m.RemoveEdge(u, v)
				}
				_ = m.Order()
			}
		}(worker)
	}
	wg.Wait()

	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[int]bool{}
	for x := 0; x < m.created; x++ {
		if seen[m.ord[x]] {
			t.Fatalf("duplicate ordinal %d", m.ord[x])
		}
		seen[m.ord[x]] = true
		for y := range m.out[x] {
			if m.ord[x] >= m.ord[y] {
				t.Fatalf("invalid edge %d(%d)->%d(%d)", x, m.ord[x], y, m.ord[y])
			}
		}
	}
}
