package paxos

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// Concurrent Plan calls: at most one succeeds, the rest see ErrClosed.
func TestConcurrentPlanOnlyOneSucceeds(t *testing.T) {
	p := mustPlanner(t, 5, 3)
	mustAddPromise(t, p, 0, report(5, 0, map[int]AcceptedValue{1: {Ballot: 1, Value: "a"}}))
	mustAddPromise(t, p, 1, report(5, 1, nil))

	const callers = 16
	var wg sync.WaitGroup
	results := make([]error, callers)
	plans := make([]RecoveryPlan, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			plans[i], results[i] = p.Plan()
		}(i)
	}
	wg.Wait()

	succeeded := 0
	var first RecoveryPlan
	for i, err := range results {
		switch {
		case err == nil:
			if succeeded == 0 {
				first = plans[i]
			} else if !reflect.DeepEqual(plans[i], first) {
				t.Fatalf("inconsistent successful plans")
			}
			succeeded++
		case errors.Is(err, ErrClosed):
		default:
			t.Fatalf("Plan returned unexpected error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d Plans succeeded, want exactly 1", succeeded)
	}
}

// Concurrent AddPromise calls from distinct acceptors all succeed and
// are equivalent to some serial order.
func TestConcurrentAddPromise(t *testing.T) {
	const n = 9
	p := mustPlanner(t, 6, n)
	var wg sync.WaitGroup
	errs := make([]error, n)
	for from := 0; from < n; from++ {
		wg.Add(1)
		go func(from int) {
			defer wg.Done()
			errs[from] = p.AddPromise(from, report(6, 0, map[int]AcceptedValue{
				from + 1: {Ballot: 1, Value: "v"},
			}))
		}(from)
	}
	wg.Wait()
	for from, err := range errs {
		if err != nil {
			t.Fatalf("AddPromise(%d) failed: %v", from, err)
		}
	}
	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plan.Entries) != n || plan.Start != 1 || plan.NextFree != n+1 {
		t.Fatalf("plan %+v, want %d entries, start=1, nextFree=%d", plan, n, n+1)
	}
}
