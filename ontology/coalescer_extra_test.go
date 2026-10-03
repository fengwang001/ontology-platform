package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestBatchLeavesCandidateThenReusesReadyAfterGridDelay(t *testing.T) {
	c, err := NewCoalescer(4, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "a", 20, 0, 0)
	mustAdd(t, c, "b", 20, 0, 0)

	requireWake(t, c, WakeResult{At: 0, Fired: []FiredEvent{{ID: "a"}}, Left: 1})
	requireWake(t, c, WakeResult{At: 4, Fired: []FiredEvent{{ID: "b", Late: 4, Skip: 0}}, Left: 0})

	next, err := c.Next()
	if err != nil || next != 20 {
		t.Fatalf("Next() = (%d,%v), want 20", next, err)
	}
}

func TestRemoveRecomputesEarliestAndReadyCandidate(t *testing.T) {
	c, err := NewCoalescer(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "a", 10, 0, 0)
	mustAdd(t, c, "b", 10, 0, 5)
	if err := c.Remove("a"); err != nil {
		t.Fatal(err)
	}
	next, err := c.Next()
	if err != nil || next != 5 {
		t.Fatalf("Next()=(%d,%v), want 5", next, err)
	}
	requireWake(t, c, WakeResult{At: 5, Fired: []FiredEvent{{ID: "b"}}})

	mustAdd(t, c, "left", 10, 2, 7)
	mustAdd(t, c, "gone", 10, 2, 7)
	next, err = c.Next()
	if err != nil || next != 9 {
		t.Fatalf("Next()=(%d,%v), want 9", next, err)
	}
	mustAdd(t, c, "survivor", 10, 2, 7)
	if err := c.Remove("survivor"); err != nil {
		t.Fatal(err)
	}
	requireWake(t, c, WakeResult{At: 9, Fired: []FiredEvent{{ID: "gone"}, {ID: "left"}}, Left: 0})
	if err := c.Remove("b"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("gone"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("left"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Next(); !errors.Is(err, ErrNoTimers) {
		t.Fatalf("Next() after removing all timers = %v", err)
	}
}

func TestAddNominalExactlyCurrentClock(t *testing.T) {
	c, err := NewCoalescer(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "a", 10, 0, 0)
	requireWake(t, c, WakeResult{At: 0, Fired: []FiredEvent{{ID: "a"}}})
	mustAdd(t, c, "b", 10, 2, 0)
	next, err := c.Next()
	if err != nil || next != 2 {
		t.Fatalf("Next()=(%d,%v), want 2", next, err)
	}
	requireWake(t, c, WakeResult{At: 2, Fired: []FiredEvent{{ID: "b"}}})
}

func TestCapacityAndStats(t *testing.T) {
	c, err := NewCoalescer(0, 64)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		mustAdd(t, c, fmt.Sprintf("t%02d", i), 10, 0, 0)
	}
	if err := c.Add("x", 10, 0, 0); !errors.Is(err, ErrTimerCapacity) {
		t.Fatalf("65th Add error = %v", err)
	}
	result, err := c.Wake()
	if err != nil || len(result.Fired) != 64 || result.Left != 0 {
		t.Fatalf("Wake() = (%+v,%v)", result, err)
	}
	stats := c.Stats()
	if stats.WakeCount != 1 || stats.FiredCount != 64 || stats.LateCount != 0 || stats.SkippedCount != 0 {
		t.Fatalf("Stats() = %+v", stats)
	}
}

func TestAdvanceToStopsAtWakeLimitWithoutError(t *testing.T) {
	c, err := NewCoalescer(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, c, "a", 1, 0, 0)
	results, err := c.AdvanceTo(maxAdvanceWakes + 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != maxAdvanceWakes {
		t.Fatalf("len(results) = %d, want %d", len(results), maxAdvanceWakes)
	}
	if results[len(results)-1].At != maxAdvanceWakes-1 {
		t.Fatalf("last wake = %d, want %d", results[len(results)-1].At, maxAdvanceWakes-1)
	}
}

func TestConcurrentOperationsAreSafe(t *testing.T) {
	c, err := NewCoalescer(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("w%02d-%02d", worker, i)
				if err := c.Add(id, 5, 2, uint64(i)); errors.Is(err, ErrTimerCapacity) {
					_ = c.Remove(id)
				}
				_, _ = c.Next()
				_, _ = c.Wake()
				_ = c.Remove(id)
				_ = c.Stats()
			}
		}(worker)
	}
	wg.Wait()
}
