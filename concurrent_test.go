package ontology

import (
	"errors"
	"sync"
	"testing"
)

// Concurrent rollback triggers: each compensation runs exactly once, the
// trace contains no duplicate step numbers, and every caller gets the same
// aggregate without duplicated entries.
func TestConcurrentRollbackRunsOnce(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	const n = 8
	var counters [n]int
	var counterMu sync.Mutex

	for i := 1; i <= n; i++ {
		i := i
		u.Add(func() error { return nil }, func() error {
			counterMu.Lock()
			counters[i-1]++
			counterMu.Unlock()
			if i%3 == 0 {
				return errors.New("fail")
			}
			return nil
		})
	}

	const callers = 32
	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for g := 0; g < callers; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = u.Rollback()
		}(g)
	}
	close(start)
	wg.Wait()

	counterMu.Lock()
	for i, c := range counters {
		if c != 1 {
			t.Fatalf("step %d compensation ran %d times, want 1", i+1, c)
		}
	}
	counterMu.Unlock()

	trace := u.Trace()
	if len(trace) != n {
		t.Fatalf("trace len = %d, want %d", len(trace), n)
	}
	seen := make(map[int]bool, len(trace))
	for _, s := range trace {
		if seen[s] {
			t.Fatalf("duplicate step %d in trace %v", s, trace)
		}
		seen[s] = true
	}

	var agg *AggregateError
	for _, err := range errs {
		if !errors.As(err, &agg) {
			t.Fatalf("every caller gets *AggregateError, got %v", err)
		}
	}
	failures := agg.Failures()
	failSeen := make(map[int]bool, len(failures))
	for _, f := range failures {
		if failSeen[f.Step] {
			t.Fatalf("step %d aggregated twice", f.Step)
		}
		failSeen[f.Step] = true
	}
}
