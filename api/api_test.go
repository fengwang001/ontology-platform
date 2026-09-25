package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// TestRejections pins invariant 4: every rejected operation fails with
// its own distinct sentinel error and leaves the state untouched, and
// the aggregator stays usable afterwards.
func TestRejections(t *testing.T) {
	cases := []struct {
		name string
		op   func(a *api.Agg) error
		want error
	}{
		{"self exchange", func(a *api.Agg) error { return a.Exchange("x", "x") }, api.ErrSelfExchange},
		{"missing node", func(a *api.Agg) error { return a.Exchange("x", "ghost") }, api.ErrNotFound},
		{"missing value", func(a *api.Agg) error { _, err := a.Value("ghost"); return err }, api.ErrNotFound},
		{"duplicate add", func(a *api.Agg) error { return a.Add("x", 9) }, api.ErrDuplicate},
		{"empty id", func(a *api.Agg) error { return a.Add("", 1) }, api.ErrEmptyID},
	}
	for i := range cases { // sentinel errors must be mutually distinct
		for j := range cases {
			if i != j && cases[i].want != cases[j].want && errors.Is(cases[i].want, cases[j].want) {
				t.Fatalf("errors %q and %q are not distinct", cases[i].name, cases[j].name)
			}
		}
	}
	for _, c := range cases {
		a := api.New()
		for _, n := range []struct {
			id string
			v  int
		}{{"x", 4}, {"y", 6}} {
			if err := a.Add(n.id, n.v); err != nil {
				t.Fatal(err)
			}
		}
		sum, spread := a.Sum(), a.Spread()
		if err := c.op(a); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
		if a.Sum() != sum || a.Spread() != spread {
			t.Fatalf("%s: rejected op changed sum/spread", c.name)
		}
		if v, err := a.Value("x"); err != nil || v != 4 {
			t.Fatalf("%s: rejected op changed x to %d", c.name, v)
		}
		if err := a.Exchange("x", "y"); err != nil { // still usable
			t.Fatalf("%s: unusable after rejection: %v", c.name, err)
		}
	}
}

// TestConcurrentExchangeSumConserved: N goroutines exchange disjoint
// node pairs while readers sample Sum concurrently; the sum must equal
// the initial total at every observation. No sleeps: a start barrier
// and WaitGroups provide the synchronization.
func TestConcurrentExchangeSumConserved(t *testing.T) {
	const pairs = 64
	a := api.New()
	total := 0
	for k := 0; k < 2*pairs; k++ {
		v := k*3 - 100
		total += v
		if err := a.Add(fmt.Sprintf("n%03d", k), v); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errs := make(chan error, 2*pairs)
	var wg sync.WaitGroup
	for p := 0; p < pairs; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			for r := 0; r < 50; r++ {
				if err := a.Exchange(fmt.Sprintf("n%03d", 2*p), fmt.Sprintf("n%03d", 2*p+1)); err != nil {
					errs <- err
					return
				}
			}
		}(p)
	}
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	for r := 0; r < 4; r++ { // concurrent readers
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
					if got := a.Sum(); got != total {
						errs <- fmt.Errorf("concurrent read: sum=%d want %d", got, total)
						return
					}
					_ = a.Spread()
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(stop)
	rwg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := a.Sum(); got != total {
		t.Fatalf("final sum=%d want %d", got, total)
	}
}

// TestSelfCheck runs the built-in self-verification of all invariants.
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
