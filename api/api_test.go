package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

func TestSelfCheck(t *testing.T) {
	a, err := api.New(0.25)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestEightStepThroughAPI(t *testing.T) {
	// Same built-in sequence as NOTES.md; every post-op value is asserted.
	steps := []struct {
		isAdd bool
		v     float64
		want  float64
	}{
		{true, 10, 10}, {true, 20, 12.5}, {true, 10, 11.875}, {true, 30, 16.40625},
		{false, 10, 16.875}, {true, 40, 22.65625}, {false, 20, 21.25}, {true, 50, 28.4375},
	}
	a, _ := api.New(0.25)
	for i, st := range steps {
		if st.isAdd {
			a.Add(st.v)
		} else if err := a.Retract(st.v); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if !a.Initialized() || a.Value() != st.want {
			t.Fatalf("step %d: Value=%v want %v", i+1, a.Value(), st.want)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	// Feed one instance, then have N goroutines read concurrently. A start
	// barrier releases them simultaneously; no sleep is used to order things.
	a, _ := api.New(0.25)
	for _, v := range []float64{10, 20, 10, 30, 40} {
		a.Add(v)
	}
	const n = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]float64, n)
	errs := make([]error, n)
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			if !a.Initialized() {
				errs[g] = errors.New("Initialized=false while fed")
				return
			}
			results[g] = a.Value()
		}(g)
	}
	close(start)
	wg.Wait()
	want := results[0]
	for g := 0; g < n; g++ {
		if errs[g] != nil {
			t.Fatal(errs[g])
		}
		if results[g] != want {
			t.Fatalf("reader %d got %v, others got %v", g, results[g], want)
		}
	}
}

func TestConcurrentSelfCheck(t *testing.T) {
	// SelfCheck must also be safe to invoke concurrently; it builds its own
	// local instance, so N simultaneous calls on one receiver must all pass.
	a, _ := api.New(0.25)
	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func() {
			defer wg.Done()
			<-start
			errs <- a.SelfCheck()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent SelfCheck failed: %v", err)
		}
	}
}

func TestPublicErrorsAndNoTrace(t *testing.T) {
	cases := []struct {
		alpha float64
		want  error
	}{
		{0, api.ErrInvalidAlpha}, {-1, api.ErrInvalidAlpha},
		{1.5, api.ErrInvalidAlpha},
	}
	for _, tc := range cases {
		if a, err := api.New(tc.alpha); !errors.Is(err, tc.want) || a != nil {
			t.Errorf("alpha=%v: want %v, got %v", tc.alpha, tc.want, err)
		}
	}
	a, _ := api.New(0.25)
	if err := a.Retract(1); !errors.Is(err, api.ErrRetractEmpty) {
		t.Errorf("empty retract: want ErrRetractEmpty, got %v", err)
	}
	a.Add(7)
	before := a.Value()
	if err := a.Retract(9); !errors.Is(err, api.ErrRetractNotFound) {
		t.Errorf("missing retract: want ErrRetractNotFound, got %v", err)
	}
	if a.Value() != before || !a.Initialized() {
		t.Fatal("rejected operation changed state")
	}
	a.Add(3) // instance remains usable after rejection
	if a.Value() != 0.25*3+0.75*7 {
		t.Fatalf("unusable after rejection: %v", a.Value())
	}
	// The three failure modes must be mutually distinguishable.
	if errors.Is(api.ErrInvalidAlpha, api.ErrRetractEmpty) ||
		errors.Is(api.ErrRetractEmpty, api.ErrRetractNotFound) ||
		errors.Is(api.ErrInvalidAlpha, api.ErrRetractNotFound) {
		t.Fatal("sentinel errors not distinct")
	}
}
