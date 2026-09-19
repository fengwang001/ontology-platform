package topk

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentPushNoLossNoDup hammers one selector from many
// goroutines with disjoint ID ranges plus shared overwritten IDs,
// then checks size, uniqueness, and rank-order validity.
func TestConcurrentPushNoLossNoDup(t *testing.T) {
	const (
		workers   = 8
		perWorker = 5000
		k         = 50
	)
	s, err := New(k, Desc)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				s.Push(fmt.Sprintf("w%d-%d", w, i), float64(i))
				s.Push("shared", float64(w*perWorker+i))
			}
		}(w)
	}
	wg.Wait()
	if n := s.Len(); n != k {
		t.Fatalf("Len = %d, want %d", n, k)
	}
	snap := s.Snapshot()
	if len(snap) != k {
		t.Fatalf("snapshot len = %d, want %d", len(snap), k)
	}
	seen := map[string]bool{}
	for i, it := range snap {
		if seen[it.ID] {
			t.Fatalf("duplicate ID %q", it.ID)
		}
		seen[it.ID] = true
		if i > 0 && !rankedBeforeOrEqual(snap[i-1], it) {
			t.Fatalf("snapshot out of order at %d: %v then %v", i, snap[i-1], it)
		}
	}
}

func rankedBeforeOrEqual(a, b Item) bool {
	return better(a, b, Desc) || a == b
}

// TestConcurrentSnapshotConsistency runs Snapshots concurrently with
// Pushes and verifies every observed snapshot is internally ordered
// and duplicate-free (no half-updated state is ever visible).
func TestConcurrentSnapshotConsistency(t *testing.T) {
	s, err := New(20, Asc)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				s.Push(fmt.Sprintf("g%d-%d", g, i%300), float64(i%97))
			}
		}(g)
	}
	for i := 0; i < 2000; i++ {
		snap := s.Snapshot()
		seen := map[string]bool{}
		for j, it := range snap {
			if seen[it.ID] {
				t.Fatalf("duplicate ID %q in concurrent snapshot", it.ID)
			}
			seen[it.ID] = true
			if j > 0 && better(it, snap[j-1], Asc) {
				t.Fatalf("disordered concurrent snapshot at %d", j)
			}
		}
		if len(snap) > 20 {
			t.Fatalf("snapshot len %d exceeds K", len(snap))
		}
	}
	close(stop)
	wg.Wait()
}
