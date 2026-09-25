package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

// Concurrency: N goroutines read the same filled instance; every
// observed Value must be identical. No sleeps; a barrier starts all
// goroutines together.
func TestConcurrentReadsConsistent(t *testing.T) {
	a, err := api.New(0.25)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{10, 20, 10, 30} {
		a.Add(v)
	}
	a.Retract(10)
	const want = 16.875

	const n = 64
	start := make(chan struct{})
	vals := make([]float64, n)
	inits := make([]bool, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				vals[k] = a.Value()
				inits[k] = a.Initialized()
				if err := a.SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %v", err)
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := range vals {
		if vals[i] != want || !inits[i] {
			t.Fatalf("goroutine %d saw value=%v init=%v, want %v/true", i, vals[i], inits[i], want)
		}
	}
}

// The three failure modes map to three mutually distinct sentinels.
func TestErrorsDistinct(t *testing.T) {
	_, errAlpha := api.New(0)
	_, errAlpha2 := api.New(-1)
	_, errAlpha3 := api.New(1.0001)
	for _, e := range []error{errAlpha, errAlpha2, errAlpha3} {
		if !errors.Is(e, api.ErrInvalidAlpha) {
			t.Fatalf("bad alpha: %v", e)
		}
	}
	a, _ := api.New(0.5)
	errEmpty := a.Retract(1)
	a.Add(1)
	errNotFound := a.Retract(2)
	if !errors.Is(errEmpty, api.ErrEmpty) || !errors.Is(errNotFound, api.ErrNotFound) {
		t.Fatalf("empty=%v notfound=%v", errEmpty, errNotFound)
	}
	pairs := [][2]error{
		{api.ErrInvalidAlpha, api.ErrNotFound},
		{api.ErrNotFound, api.ErrEmpty},
		{api.ErrEmpty, api.ErrInvalidAlpha},
	}
	for _, p := range pairs {
		if errors.Is(p[0], p[1]) || p[0] == p[1] {
			t.Fatalf("sentinels not distinct: %v vs %v", p[0], p[1])
		}
	}
	// Rejected ops leave the instance usable and unchanged.
	a.Retract(1) // drain: now empty again
	if a.Initialized() {
		t.Fatal("state changed by rejected ops")
	}
}

// SelfCheck must pass on a fresh instance and under repetition.
func TestSelfCheck(t *testing.T) {
	for _, alpha := range []float64{0.1, 0.25, 1} {
		a, err := api.New(alpha)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if err := a.SelfCheck(); err != nil {
				t.Fatalf("alpha=%v: %v", alpha, err)
			}
		}
	}
}
