package mvcc

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentDeleteExactlyOneSucceeds(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.Insert("t", 1, 0))
	const deleters = 16
	for i := 0; i < deleters; i++ {
		mustOK(t, s.Begin(100+i))
	}
	var wg sync.WaitGroup
	results := make(chan error, deleters)
	for i := 0; i < deleters; i++ {
		wg.Add(1)
		go func(x int) {
			defer wg.Done()
			results <- s.Delete("t", x, 0)
		}(100 + i)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrTupleDeleted) {
			t.Fatalf("unexpected delete error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 successful delete, got %d", successes)
	}
}

func TestConcurrentSnapshotsSequentialIDs(t *testing.T) {
	s := NewStore()
	const n = 64
	var wg sync.WaitGroup
	ids := make(chan int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- s.Snapshot()
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[int]bool)
	for id := range ids {
		if id < 1 || id > n {
			t.Fatalf("snapshot id %d out of range [1,%d]", id, n)
		}
		if seen[id] {
			t.Fatalf("duplicate snapshot id %d", id)
		}
		seen[id] = true
	}
	for id := 1; id <= n; id++ {
		if !seen[id] {
			t.Fatalf("missing snapshot id %d", id)
		}
	}
}

func TestConcurrentMixedOps(t *testing.T) {
	s := NewStore()
	mustOK(t, s.Begin(1))
	mustOK(t, s.BeginSub(2, 1))
	mustOK(t, s.Insert("t", 2, 0))
	snap := s.Snapshot()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = s.Visible("t", 1, j, snap)
				_ = s.Insert("u", 2, j+1)
				_ = s.Delete("t", 2, j+1)
				s.Snapshot()
			}
		}(i)
	}
	wg.Wait()
}
