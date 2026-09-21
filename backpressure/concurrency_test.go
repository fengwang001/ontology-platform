package backpressure

import (
	"sync"
	"sync/atomic"
	"testing"
)

// Semantics 8: concurrent Add/Sub/Stat must be race-clean and conserve
// the level: level == accepted adds - valid subs (never negative), and
// Pauses/Resumes must be consistent with the current state.
func TestConcurrentConservation(t *testing.T) {
	c := mustNew(t, 100, 500, 100000)
	var accepted atomic.Int64
	var subbed atomic.Int64

	const workers = 16
	const rounds = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if c.Add(3) {
					accepted.Add(3)
					c.Sub(3) // only sub what this goroutine added
					subbed.Add(3)
				}
				_ = c.Stat()
			}
		}()
	}
	wg.Wait()

	r := c.Stat()
	want := accepted.Load() - subbed.Load()
	if r.Level != want {
		t.Fatalf("level = %d, want accepted(%d) - subbed(%d) = %d",
			r.Level, accepted.Load(), subbed.Load(), want)
	}
	if r.Level < 0 {
		t.Fatalf("level must never be negative, got %d", r.Level)
	}
	switch r.State {
	case Paused:
		if r.Pauses != r.Resumes+1 {
			t.Fatalf("Paused requires Pauses == Resumes+1, got %+v", r)
		}
	case Flowing:
		if r.Pauses != r.Resumes {
			t.Fatalf("Flowing requires Pauses == Resumes, got %+v", r)
		}
	}
	if want != 0 {
		t.Fatalf("paired add/sub should drain to 0, got %d", want)
	}
}

// Concurrent callbacks calling Stat must not deadlock or race.
func TestConcurrentCallbacksAndStat(t *testing.T) {
	c := mustNew(t, 10, 50, 1000)
	var calls atomic.Int64
	c.OnChange(func(s State) {
		_ = c.Stat()
		calls.Add(1)
	})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if c.Add(1) {
					c.Sub(1)
				}
			}
		}()
	}
	wg.Wait()
	r := c.Stat()
	if int64(r.Pauses+r.Resumes) != calls.Load() {
		t.Fatalf("transitions = %d, callback calls = %d, must match",
			r.Pauses+r.Resumes, calls.Load())
	}
}
