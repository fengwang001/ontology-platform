package cluster

import (
	"sort"
	"sync"
	"testing"
)

// Semantics 8: concurrent Propose is race-clean, indexes are strictly
// increasing with no duplicates or holes, Commit is monotonic, and the
// durability (1) / no-fork (7) invariants hold throughout.
func TestConcurrentPropose(t *testing.T) {
	const (
		n         = 5
		workers   = 8
		perWorker = 25
	)
	c := New(n, testClock())
	var wg sync.WaitGroup
	idxCh := make(chan uint64, workers*perWorker)
	commitCh := make(chan uint64, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				idx, ok, err := c.Propose(1, "payload")
				if err != nil {
					t.Errorf("propose: %v", err)
					return
				}
				if !ok {
					t.Errorf("propose %d not committed with no faults", idx)
				}
				idxCh <- idx
				commitCh <- c.Commit()
			}
		}(w)
	}
	wg.Wait()
	close(idxCh)
	close(commitCh)

	// Indexes: exactly 1..workers*perWorker, no duplicates, no holes.
	var indexes []uint64
	for idx := range idxCh {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	if len(indexes) != workers*perWorker {
		t.Fatalf("got %d indexes, want %d", len(indexes), workers*perWorker)
	}
	for i, idx := range indexes {
		if idx != uint64(i)+1 {
			t.Fatalf("index sequence broken at %d: got %d", i+1, idx)
		}
	}

	// Commit was monotonically non-decreasing and ends at the last index.
	var prev uint64
	for cm := range commitCh {
		if cm < prev {
			t.Fatalf("Commit regressed: %d after %d", cm, prev)
		}
		prev = cm
	}
	if got := c.Commit(); got != uint64(workers*perWorker) {
		t.Fatalf("final Commit = %d, want %d", got, workers*perWorker)
	}

	// Invariant 1: every committed entry sits on a majority of replicas.
	// Invariant 7: all committed snapshots are identical (prefix-free).
	want := c.Snapshot(0)
	if len(want) != workers*perWorker {
		t.Fatalf("snapshot len = %d, want %d", len(want), workers*perWorker)
	}
	for id := 1; id < n; id++ {
		s := c.Snapshot(id)
		if len(s) != len(want) {
			t.Fatalf("replica %d snapshot len = %d, want %d", id, len(s), len(want))
		}
		for k := range s {
			if s[k] != want[k] {
				t.Fatalf("replica %d diverges at %d", id, k)
			}
		}
	}

	// ReadIndex still agrees with Commit.
	ri, err := c.ReadIndex()
	if err != nil || ri < c.Commit() {
		t.Fatalf("ReadIndex = %d, %v; Commit = %d", ri, err, c.Commit())
	}
}
