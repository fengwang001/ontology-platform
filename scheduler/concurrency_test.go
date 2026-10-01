package scheduler

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentOperationsMaintainInvariant(t *testing.T) {
	q := mustNewQueue(t, 10, 1000, 500)
	var wg sync.WaitGroup
	var clock sync.Mutex
	current := int64(0)

	nextNow := func(add int64) int64 {
		clock.Lock()
		defer clock.Unlock()
		current += add
		return current
	}

	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 100; step++ {
				id := fmt.Sprintf("w%d-p%d", worker, step%8)
				now := nextNow(1)
				_ = q.Add(id, step%7, now)

				_, _, _ = q.Pop(nextNow(1))
				_ = q.Event(int64(1<<(step%8)), nextNow(1))

				outcome := OutcomeFailed
				if step%4 == 0 {
					outcome = OutcomeScheduled
				}
				_ = q.Done(id, outcome, int64(1<<(step%8)), nextNow(1))
				_ = q.Advance(nextNow(2))
				if step%5 == 0 {
					_ = q.Remove(id)
				}
				q.mu.Lock()
				alive := len(q.pods)
				sizes := Sizes{
					Active:        q.active.Len(),
					Backoff:       q.backoff.Len(),
					Unschedulable: q.unschedulable.Len(),
				}
				sizes.InFlight = alive - sizes.Active - sizes.Backoff - sizes.Unschedulable
				q.mu.Unlock()
				if sizes.Active+sizes.Backoff+sizes.Unschedulable+sizes.InFlight != alive {
					t.Errorf("invariant broken: sizes=%+v pods=%d", sizes, alive)
					return
				}
			}
		}(worker)
	}

	wg.Wait()
	q.mu.Lock()
	alive := len(q.pods)
	sizes := Sizes{
		Active:        q.active.Len(),
		Backoff:       q.backoff.Len(),
		Unschedulable: q.unschedulable.Len(),
	}
	sizes.InFlight = alive - sizes.Active - sizes.Backoff - sizes.Unschedulable
	q.mu.Unlock()
	if total := sizes.Active + sizes.Backoff + sizes.Unschedulable + sizes.InFlight; total != alive {
		t.Fatalf("final invariant broken: sizes=%+v pods=%d", sizes, alive)
	}
}
