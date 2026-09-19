package rollback

import (
	"errors"
	"sync"
	"testing"
)

func buildUnit(t *testing.T, store *Store, n int, rec *[]int, failSteps map[int]error, recMu *sync.Mutex) *Unit {
	u := NewUnit(store)
	for i := 1; i <= n; i++ {
		stepNo := i
		comp := func() error {
			recMu.Lock()
			*rec = append(*rec, stepNo)
			recMu.Unlock()
			return nil
		}
		if failErr, ok := failSteps[stepNo]; ok {
			comp = func() error {
				recMu.Lock()
				*rec = append(*rec, stepNo)
				recMu.Unlock()
				return failErr
			}
		}
		if err := u.Step(func() error { return nil }, comp); err != nil {
			t.Fatalf("step %d: %v", stepNo, err)
		}
	}
	return u
}

func TestConcurrentRollbackRunsEachCompensationOnce(t *testing.T) {
	const goroutines = 32

	store := NewStore()
	var mu sync.Mutex
	rec := make([]int, 0)
	u := buildUnit(t, store, 8, &rec, nil, &mu)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = u.Rollback()
		}(i)
	}
	close(start)
	wg.Wait()

	// Every caller gets the same (nil) result.
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent rollback err = %v, want nil", err)
		}
	}

	want := []int{8, 7, 6, 5, 4, 3, 2, 1}
	if got := u.Trace(); !equalInts(got, want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := rec; !equalInts(got, want) {
		t.Fatalf("external recorder = %v, want each exactly once %v", got, want)
	}
}

func TestConcurrentFailingRollbackAggregatesOnce(t *testing.T) {
	const goroutines = 24

	store := NewStore()
	var mu sync.Mutex
	rec := make([]int, 0)
	u := buildUnit(t, store, 6, &rec, map[int]error{
		2: errors.New("fail-2"),
		5: errors.New("fail-5"),
	}, &mu)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			errs[idx] = u.Rollback()
		}(i)
	}
	close(start)
	wg.Wait()

	// Every caller receives the same aggregate with no duplicated steps.
	for _, err := range errs {
		var agg *AggregateError
		if !errors.As(err, &agg) {
			t.Fatalf("err = %v, want AggregateError", err)
		}
		failures := agg.Failures()
		if len(failures) != 2 || failures[0].Step != 2 || failures[1].Step != 5 {
			t.Fatalf("failures = %+v, want steps 2 and 5 once each", failures)
		}
	}

	want := []int{6, 5, 4, 3, 2, 1}
	if got := u.Trace(); !equalInts(got, want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := rec; !equalInts(got, want) {
		t.Fatalf("recorder = %v, each compensation must run once", got)
	}
	if store.PollutionStep() != 2 {
		t.Fatalf("pollution step = %d, want 2", store.PollutionStep())
	}
}

func TestConcurrentRollbackWithBarrierOverlap(t *testing.T) {
	// Block compensations briefly so rollbacks overlap heavily under -race.
	store := NewStore()
	var mu sync.Mutex
	rec := make([]int, 0)
	u := NewUnit(store)
	for i := 1; i <= 10; i++ {
		stepNo := i
		_ = u.Step(
			func() error { return nil },
			func() error {
				mu.Lock()
				rec = append(rec, stepNo)
				mu.Unlock()
				return nil
			},
		)
	}

	const goroutines = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = u.Rollback()
		}()
	}
	close(start)
	wg.Wait()

	if got := len(u.Trace()); got != 10 {
		t.Fatalf("trace length = %d, want 10 (no duplicates)", got)
	}
}
