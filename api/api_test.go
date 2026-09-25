package api

import (
	"fmt"
	"sync"
	"testing"
)

// TestSelfCheck: the built-in self-check passes on a fresh service.
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestSentinelErrorsDistinct: the four rejection kinds are distinguishable.
func TestSentinelErrorsDistinct(t *testing.T) {
	s := New()
	_ = s.Create("x")
	_ = s.Acquire("r", "x")
	errs := []error{
		s.Create(""),
		s.Create("x"),
		s.Acquire("r", "ghost"),
		s.Release("nobody", "x"),
	}
	want := []error{ErrEmpty, ErrExists, ErrNotFound, ErrNotHeld}
	for i := range want {
		if errs[i] != want[i] {
			t.Fatalf("case %d: got %v want %v", i, errs[i], want[i])
		}
		for j := range want {
			if i != j && errs[i] == want[j] {
				t.Fatalf("errors %d and %d not distinct", i, j)
			}
		}
	}
}

// TestConcurrentReads: N goroutines read RefCount/Alive/SelfCheck, all agree.
func TestConcurrentReads(t *testing.T) {
	s := New()
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("o%d", i)
		if err := s.Create(id); err != nil {
			t.Fatal(err)
		}
		for j := 0; j <= i%5; j++ {
			if err := s.Acquire(fmt.Sprintf("r%d", j), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	start := make(chan struct{})
	errs := make(chan error, 64)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < 20; i++ {
				id := fmt.Sprintf("o%d", i)
				n, err := s.RefCount(id)
				if err != nil || n != i%5+1 || !s.Alive(id) {
					errs <- fmt.Errorf("%s count=%d err=%v", id, n, err)
				}
			}
			if g%8 == 0 {
				if err := s.SelfCheck(); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
