package topk

import (
	"fmt"
	"sync"
	"testing"
)

// The selector must never hold more than K elements, no matter how long
// the stream is.
func TestLenNeverExceedsK(t *testing.T) {
	const k = 7
	s := mustNew(t, k, Desc)
	for i := 0; i < 20000; i++ {
		s.Push(fmt.Sprintf("id-%d", i), float64((i*7919)%1000))
		if got := s.Len(); got > k {
			t.Fatalf("after %d pushes Len() = %d, exceeds K=%d", i+1, got, k)
		}
	}
	if got := s.Len(); got != k {
		t.Fatalf("final Len() = %d, want %d", got, k)
	}
}

// When fewer than K elements were pushed, Snapshot returns all of them.
func TestSnapshotReturnsAllWhenUnderfull(t *testing.T) {
	s := mustNew(t, 10, Asc)
	s.Push("x", 2)
	s.Push("y", 1)
	got := s.Snapshot()
	if len(got) != 2 || got[0].ID != "y" || got[1].ID != "x" {
		t.Fatalf("snapshot = %v, want [y x]", got)
	}
}

// Mutating a returned snapshot must not disturb the selector.
func TestSnapshotIsIndependentCopy(t *testing.T) {
	s := mustNew(t, 2, Desc)
	s.Push("a", 1)
	s.Push("b", 2)
	snap := s.Snapshot()
	snap[0] = Element{ID: "corrupted", Score: -1}
	if got := s.Snapshot(); got[0].ID != "b" {
		t.Fatalf("internal state disturbed by snapshot mutation: %v", got)
	}
}

// Concurrent pushes must lose nothing and never duplicate an ID; snapshots
// taken concurrently must always observe a consistent, sorted state.
func TestConcurrentPushAndSnapshot(t *testing.T) {
	const (
		k        = 16
		workers  = 8
		perWork  = 500
		watchers = 2
	)
	s := mustNew(t, k, Desc)
	stop := make(chan struct{})
	var watchersWG sync.WaitGroup
	for w := 0; w < watchers; w++ {
		watchersWG.Add(1)
		go func() {
			defer watchersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				if len(snap) > k {
					t.Errorf("snapshot len %d exceeds K", len(snap))
					return
				}
				seen := map[string]bool{}
				for i, e := range snap {
					if seen[e.ID] {
						t.Errorf("duplicate ID %q in snapshot", e.ID)
						return
					}
					seen[e.ID] = true
					if i > 0 && !less(Desc, snap[i-1], e) {
						t.Errorf("snapshot out of order at %d: %v", i, snap)
						return
					}
				}
			}
		}()
	}
	var pushers sync.WaitGroup
	for w := 0; w < workers; w++ {
		pushers.Add(1)
		go func(w int) {
			defer pushers.Done()
			for i := 0; i < perWork; i++ {
				id := fmt.Sprintf("w%d-id%d", w, i)
				s.Push(id, float64((i*31+w)%97))
				// Re-push shared IDs to exercise concurrent
				// overwrite of the same keys.
				s.Push(fmt.Sprintf("shared-%d", i%50), float64(i))
			}
		}(w)
	}
	pushers.Wait()
	close(stop)
	watchersWG.Wait()
	if got := s.Len(); got != k {
		t.Fatalf("Len() = %d, want %d", got, k)
	}
	seen := map[string]bool{}
	for _, e := range s.Snapshot() {
		if seen[e.ID] {
			t.Fatalf("duplicate ID %q in final snapshot", e.ID)
		}
		seen[e.ID] = true
	}
}
