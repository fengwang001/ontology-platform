package cluster

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentPropose hammers the gateway from many goroutines and checks
// Index uniqueness/contiguity, commit monotonicity, and snapshot agreement.
func TestConcurrentPropose(t *testing.T) {
	c := newCluster(5)
	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[uint64]string)
	lastCommit := uint64(0)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				data := fmt.Sprintf("w%d-%d", w, i)
				idx, committed, err := c.Propose(1, data)
				if err != nil {
					t.Errorf("Propose: %v", err)
					return
				}
				if !committed {
					t.Errorf("entry %d not committed on healthy cluster", idx)
				}
				if _, err := c.ReadIndex(); err != nil {
					t.Errorf("ReadIndex: %v", err)
				}
				mu.Lock()
				if prev, dup := seen[idx]; dup {
					t.Errorf("index %d reused: %q and %q", idx, prev, data)
				}
				seen[idx] = data
				if got := c.Commit(); got < lastCommit {
					t.Errorf("commit regressed: %d < %d", got, lastCommit)
				} else {
					lastCommit = got
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	total := uint64(workers * perWorker)
	for i := uint64(1); i <= total; i++ {
		if _, ok := seen[i]; !ok {
			t.Fatalf("index %d missing: holes in allocation", i)
		}
	}
	if c.Commit() != total {
		t.Fatalf("Commit() = %d, want %d", c.Commit(), total)
	}
	base := c.Snapshot(0)
	if uint64(len(base)) != total {
		t.Fatalf("snapshot len = %d, want %d", len(base), total)
	}
	for id := 1; id < 5; id++ {
		snap := c.Snapshot(id)
		if len(snap) != len(base) {
			t.Fatalf("replica %d snapshot len = %d, want %d", id, len(snap), len(base))
		}
		for i := range base {
			if snap[i] != base[i] {
				t.Fatalf("replica %d entry %d = %q, want %q", id, i+1, snap[i], base[i])
			}
		}
	}
}
