package cluster

import (
	"sync"
	"testing"
)

// Semantic 8: concurrent proposals get strictly increasing, gap-free
// indices; the commit point is monotone; run under -race.
func TestConcurrentPropose(t *testing.T) {
	const workers = 8
	const perWorker = 25
	c := New(5, nil)
	var mu sync.Mutex
	seen := make(map[uint64]bool)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lastCommit := uint64(0)
			for i := 0; i < perWorker; i++ {
				idx, committed, err := c.Propose(1, "payload")
				if err != nil {
					t.Errorf("Propose: %v", err)
					return
				}
				if !committed {
					t.Errorf("healthy cluster must commit")
					return
				}
				ci := c.Commit()
				mu.Lock()
				if seen[idx] {
					t.Errorf("duplicate index %d", idx)
				}
				seen[idx] = true
				mu.Unlock()
				// Commit is monotone, so each goroutine's own
				// observations must be non-decreasing.
				if ci < lastCommit {
					t.Errorf("commit regressed: %d < %d", ci, lastCommit)
				}
				lastCommit = ci
			}
		}()
	}
	wg.Wait()
	total := uint64(workers * perWorker)
	for i := uint64(1); i <= total; i++ {
		if !seen[i] {
			t.Fatalf("index %d missing: hole in allocation", i)
		}
	}
	if got := c.Commit(); got != total {
		t.Fatalf("Commit = %d, want %d", got, total)
	}
	want := c.Snapshot(0)
	if len(want) != int(total) {
		t.Fatalf("snapshot len = %d, want %d", len(want), total)
	}
	for id := 1; id < 5; id++ {
		got := c.Snapshot(id)
		if len(got) != len(want) {
			t.Fatalf("replica %d snapshot len = %d, want %d", id, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("replica %d diverged at %d", id, i)
			}
		}
	}
}

// Concurrent proposals mixed with fault injection and ReadIndex calls
// must stay race-free and keep the commit point monotone.
func TestConcurrentWithFaultsAndReads(t *testing.T) {
	c := New(7, nil)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, _, err := c.Propose(1, "x"); err != nil {
					t.Errorf("Propose: %v", err)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		last := uint64(0)
		for i := 0; i < 50; i++ {
			c.SetFault(i%7, Fault(i%4), i%3)
			if ri, err := c.ReadIndex(); err == nil {
				if ri < last {
					t.Errorf("read index regressed: %d < %d", ri, last)
				}
				last = ri
			}
			if ci := c.Commit(); ci < last && last > 0 {
				t.Errorf("commit %d below read index %d", ci, last)
			}
		}
	}()
	wg.Wait()
}
