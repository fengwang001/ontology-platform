package retry

import (
	"sync"
	"testing"
	"time"
)

// Semantic 8a: a reused Runner reports only the latest run's delays.
func TestRunnerReuseResetsDelays(t *testing.T) {
	r := New(Policy{MaxAttempts: 3, Base: 10 * time.Millisecond}, func(time.Duration) {}, nil)
	r.Do(failTimes(3))
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("first run: len(Delays())=%d, want 2", got)
	}
	r.Do(failTimes(0)) // succeeds on attempt 1: no waits
	if got := len(r.Delays()); got != 0 {
		t.Fatalf("second run: len(Delays())=%d, want 0 (no accumulation)", got)
	}
	r.Do(failTimes(2)) // succeeds on attempt 3: two waits
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("third run: len(Delays())=%d, want 2", got)
	}
}

// Semantic 8b: concurrent Do calls are race-free and each goroutine
// observes its own deterministic schedule. Run with `go test -race`.
func TestConcurrentDo(t *testing.T) {
	p := Policy{MaxAttempts: 3, Base: 10 * time.Millisecond, Factor: 2}
	r := New(p, func(time.Duration) {}, nil)
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attempt, err := r.Do(failTimes(3))
			if err == nil || attempt != 3 {
				errs <- "bad result"
				return
			}
			got := r.Delays()
			if len(got) != len(want) {
				errs <- "wrong delay count"
				return
			}
			for i := range want {
				if got[i] != want[i] {
					errs <- "delay mismatch"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}
