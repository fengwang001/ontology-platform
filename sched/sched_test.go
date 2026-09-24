package sched

import (
	"runtime"
	"sync"
	"testing"
)

func TestLimiter(t *testing.T) {
	// One table of (maxConcurrency, tasks). A single test function loops over
	// every row and every task; the concurrency invariant is checked on every
	// acquisition rather than expanded into per-case functions.
	rows := []struct {
		max   int
		tasks int
	}{
		{1, 50},
		{2, 100},
		{8, 500},
		{16, 500},
	}
	for _, row := range rows {
		lim := NewLimiter(row.max)
		var wg sync.WaitGroup
		var guard sync.Mutex
		cur := 0
		for i := 0; i < row.tasks; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				lim.Acquire()
				guard.Lock()
				cur++
				if cur > row.max {
					t.Errorf("concurrency %d > max %d", cur, row.max)
				}
				guard.Unlock()
				runtime.Gosched()
				guard.Lock()
				cur--
				guard.Unlock()
				lim.Release()
			}()
		}
		wg.Wait()
		if lim.Peak() > row.max {
			t.Fatalf("max=%d peak=%d exceeds limit", row.max, lim.Peak())
		}
		if row.tasks >= row.max && lim.Peak() != row.max {
			t.Fatalf("max=%d peak=%d, want exactly %d", row.max, lim.Peak(), row.max)
		}
	}
}

func TestReadyQueue(t *testing.T) {
	// Deterministic tie-breaking: regardless of arrival batches, Pop returns
	// the globally smallest id. Assertions run in one loop.
	batches := [][]string{
		{"c", "a"},
		{"e", "b", "d"},
	}
	q := NewReadyQueue()
	for _, b := range batches {
		q.PushAll(b)
	}
	want := []string{"a", "b", "c", "d", "e"}
	for _, w := range want {
		if got := q.Pop(); got != w {
			t.Fatalf("Pop = %q, want %q", got, w)
		}
	}
	if got := q.Pop(); got != "" {
		t.Fatalf("empty queue Pop = %q, want empty", got)
	}
}

func TestDecisionCounter(t *testing.T) {
	lim := NewLimiter(4)
	// Simulate Kahn accounting over random graphs; decisions must stay under
	// 4*(V+E). Loop covers each graph instead of separate functions.
	totalVE := 0
	for g := 0; g < 30; g++ {
		v := 1 + g*3
		e := g * 5
		totalVE += v + e
		lim.Decision(e + v)
		if got := lim.Decisions(); got > 4*totalVE {
			t.Fatalf("decisions %d > 4*(%d)", got, totalVE)
		}
	}
	if lim.Peak() != 0 {
		t.Fatalf("unexpected peak %d", lim.Peak())
	}
}
