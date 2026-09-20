package retry

import (
	"sync"
	"testing"
	"time"
)

// Semantic 8a: a reused Runner does not accumulate delays across Do calls.
func TestRunnerReuseResetsDelays(t *testing.T) {
	r := New(Policy{MaxAttempts: 3, Base: 100, Factor: 2}, func(time.Duration) {}, nil)
	r.Do(failAlways(nil))
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("first run: len(Delays()) = %d, want 2", got)
	}
	r.Do(failAlways(nil))
	got := r.Delays()
	if len(got) != 2 {
		t.Fatalf("second run: len(Delays()) = %d, want 2 (no accumulation)", len(got))
	}
	want := []time.Duration{100, 200}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("second run delay[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// Semantic 8b: concurrent Do calls are race-clean and each run observes
// exactly its own delays (deterministic policy => identical sequences).
func TestConcurrentDo(t *testing.T) {
	r := New(Policy{MaxAttempts: 4, Base: 100, Factor: 2, JitterPct: 10},
		func(time.Duration) {}, func() float64 { return 0.5 })
	want := []time.Duration{100, 200, 400}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				n, err := r.Do(failAlways(nil))
				if n != 4 || err == nil {
					errs <- "bad Do result"
					return
				}
				got := r.Delays()
				if len(got) != len(want) {
					errs <- "wrong delays length"
					return
				}
				for j := range want {
					if got[j] != want[j] {
						errs <- "delays mismatch"
						return
					}
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
