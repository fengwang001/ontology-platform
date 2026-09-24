package api_test

import (
	"errors"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func sp(s string) *string { return &s }

func feed(t *testing.T) *api.Engine {
	t.Helper()
	g, err := api.New(64)
	if err != nil {
		t.Fatal(err)
	}
	for i := range int64(10) {
		if err := g.AddLeft(i, sp("k")); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddRight(sp("k")); err != nil {
		t.Fatal(err)
	}
	return g
}

// TestConcurrentViewConsistent: N goroutines read one fed instance; every
// view must be identical and SelfCheck must pass. No sleeps, start barrier.
func TestConcurrentViewConsistent(t *testing.T) {
	g := feed(t)
	want := g.View()
	start := make(chan struct{})
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			for range 100 {
				if got := g.View(); !slices.Equal(got, want) {
					errs <- errors.New("view mismatch")
					return
				}
				if err := g.SelfCheck(); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestErrorsDistinct: the failure classes are pairwise distinguishable.
func TestErrorsDistinct(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrBadMaxLeft) {
		t.Fatalf("want ErrBadMaxLeft, got %v", err)
	}
	g, err := api.New(1)
	if err != nil {
		t.Fatal(err)
	}
	_ = g.AddLeft(1, sp("a"))
	got := []error{g.AddLeft(2, sp("b")), g.AddLeft(1, sp("b")), g.DelLeft(9), g.DelRight(sp("z"))}
	want := []error{api.ErrTooManyLeft, api.ErrLeftExists, api.ErrLeftNotFound, api.ErrRefNegative}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			t.Fatalf("op %d: want %v, got %v", i, want[i], got[i])
		}
	}
	sents := append([]error{api.ErrBadMaxLeft}, want...)
	for i, a := range sents {
		for _, b := range sents[i+1:] {
			if errors.Is(a, b) || errors.Is(b, a) {
				t.Fatalf("sentinels not distinct: %v vs %v", a, b)
			}
		}
	}
}

// TestSelfCheck: the built-in self-check passes on a fresh engine.
func TestSelfCheck(t *testing.T) {
	g, err := api.New(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
