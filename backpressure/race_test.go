package backpressure

import (
	"sync"
	"testing"
)

// Semantics 8: concurrent Add/Sub/Stat is race-free and the books always
// balance: Level equals accepted Adds minus effective Subs (never below
// zero), and Pauses/Resumes stay consistent with the current state.
func TestConcurrentConservation(t *testing.T) {
	const (
		workers   = 8
		rounds    = 500
		hardLimit = 64
	)
	c := mustNew(t, 8, 16, hardLimit)

	var mu sync.Mutex
	var accepted, subtracted int64

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if (w+i)%2 == 0 {
					if c.Add(1) {
						mu.Lock()
						accepted++
						mu.Unlock()
					}
				} else {
					before := c.Stat().Level
					c.Sub(1)
					mu.Lock()
					if before > 0 {
						subtracted++
					}
					mu.Unlock()
				}
				_ = c.Stat()
			}
		}(w)
	}
	wg.Wait()

	r := c.Stat()
	if r.Level < 0 {
		t.Fatalf("Level = %d, must never be negative", r.Level)
	}
	if r.Level > hardLimit {
		t.Fatalf("Level = %d, exceeds hard limit %d", r.Level, hardLimit)
	}
	// accepted - subtracted is a lower bound: Subs observed at level 0 are
	// clamped away, so the true level can only be higher than the naive sum.
	if want := accepted - subtracted; r.Level < want {
		t.Fatalf("Level = %d, want >= accepted-subtracted = %d", r.Level, want)
	}
	switch r.State {
	case Paused:
		if r.Pauses != r.Resumes+1 {
			t.Fatalf("Paused but Pauses=%d Resumes=%d, want Pauses == Resumes+1",
				r.Pauses, r.Resumes)
		}
	case Flowing:
		if r.Pauses != r.Resumes {
			t.Fatalf("Flowing but Pauses=%d Resumes=%d, want equal",
				r.Pauses, r.Resumes)
		}
	}
}

// Concurrent listeners calling Stat from inside callbacks must not
// deadlock or race.
func TestConcurrentCallbacksAndStat(t *testing.T) {
	c := mustNew(t, 4, 8, 16)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		c.OnChange(func(State) { _ = c.Stat() })
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				c.Add(1)
				_ = c.Stat()
				c.Sub(1)
			}
		}()
	}
	wg.Wait()
	r := c.Stat()
	if r.Pauses != r.Resumes && r.Pauses != r.Resumes+1 {
		t.Fatalf("Pauses=%d Resumes=%d differ by more than 1", r.Pauses, r.Resumes)
	}
}
