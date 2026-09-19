package topk

import (
	"fmt"
	"sync"
	"testing"
)

// Many goroutines pushing disjoint ID ranges must not lose elements and
// must never hold more than K.
func TestConcurrentPushNoLossNoOverflow(t *testing.T) {
	const (
		k         = 50
		workers   = 8
		perWorker = 2000
		totalIDs  = workers * perWorker
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
				s.Push(fmt.Sprintf("w%02d-%05d", w, i), float64((w*perWorker+i)%7919))
			}
		}(w)
	}
	wg.Wait()
	if n := s.Size(); n != k {
		t.Fatalf("Size()=%d, want %d (total distinct IDs=%d)", n, k, totalIDs)
	}
	snap := s.Snapshot()
	seen := make(map[string]bool, len(snap))
	for _, e := range snap {
		if seen[e.ID] {
			t.Fatalf("duplicate ID in snapshot: %q", e.ID)
		}
		seen[e.ID] = true
	}
}

// Concurrent pushes of the same IDs from several goroutines must converge
// to one entry per ID.
func TestConcurrentPushSameIDsNoDuplicates(t *testing.T) {
	const workers = 16
	s, err := New(10, Asc)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				s.Push(fmt.Sprintf("id-%d", i%30), float64(i))
			}
		}(w)
	}
	wg.Wait()
	snap := s.Snapshot()
	seen := make(map[string]bool, len(snap))
	for _, e := range snap {
		if seen[e.ID] {
			t.Fatalf("duplicate ID after concurrent overwrite: %q", e.ID)
		}
		seen[e.ID] = true
	}
}

// Snapshots taken while pushes are in flight must always observe a
// consistent, fully sorted state with no holes and no duplicates.
func TestConcurrentSnapshotConsistency(t *testing.T) {
	const k = 20
	s, err := New(k, Desc)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				s.Push(fmt.Sprintf("w%d-%d", w, i), float64(i%65536))
			}
		}(w)
	}
	for round := 0; round < 2000; round++ {
		snap := s.Snapshot()
		if len(snap) > k {
			t.Fatalf("snapshot len=%d exceeds K=%d", len(snap), k)
		}
		seen := make(map[string]bool, len(snap))
		for i, e := range snap {
			if seen[e.ID] {
				t.Fatalf("duplicate ID %q in concurrent snapshot", e.ID)
			}
			seen[e.ID] = true
			if i > 0 && s.less(e, snap[i-1]) {
				t.Fatalf("snapshot out of order at %d: %v then %v", i, snap[i-1], e)
			}
		}
	}
	close(stop)
	wg.Wait()
}

// A returned snapshot must be isolated from later mutations.
func TestSnapshotIsACopy(t *testing.T) {
	s, err := New(3, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("a", 1.0)
	s.Push("b", 2.0)
	snap := s.Snapshot()
	snap[0].ID = "corrupted"
	snap[0].Score = -999
	s.Push("c", 3.0)
	got := snapshotIDs(s)
	for _, id := range got {
		if id == "corrupted" {
			t.Fatalf("mutating the returned snapshot leaked into the selector: %v", got)
		}
	}
}
