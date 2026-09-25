package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// TestSelfCheck: the built-in self-check must pass on a fresh service.
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestSevenStep replays the NOTES.md derivation through the public API.
func TestSevenStep(t *testing.T) {
	s := api.New()
	if err := s.Create("X"); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		op      string
		ref     string
		wantN   int
		alive   bool
		wantErr error
	}{
		{"acq", "r1", 1, true, nil},
		{"acq", "r2", 2, true, nil},
		{"acq", "r2", 2, true, nil}, // idempotent
		{"rel", "r1", 1, true, nil}, // shared survives
		{"rel", "r2", 0, false, nil},
		{"acq", "r3", 0, false, api.ErrNotFound}, // no resurrection
	}
	for i, st := range steps {
		var err error
		if st.op == "acq" {
			err = s.Acquire(st.ref, "X")
		} else {
			err = s.Release(st.ref, "X")
		}
		if !errors.Is(err, st.wantErr) {
			t.Fatalf("step %d: err=%v want %v", i+2, err, st.wantErr)
		}
		n, _ := s.RefCount("X")
		if n != st.wantN || s.Alive("X") != st.alive {
			t.Fatalf("step %d: n=%d alive=%v, want n=%d alive=%v",
				i+2, n, s.Alive("X"), st.wantN, st.alive)
		}
	}
}

// TestSentinelsDistinct: the four error classes are mutually distinct.
func TestSentinelsDistinct(t *testing.T) {
	errs := []error{api.ErrEmpty, api.ErrExists, api.ErrNotFound, api.ErrNotHeld}
	seen := map[error]bool{}
	for _, e := range errs {
		if seen[e] {
			t.Fatalf("duplicate sentinel: %v", e)
		}
		seen[e] = true
	}
	s := api.New()
	_ = s.Create("Z")
	_ = s.Acquire("a", "Z")
	if !errors.Is(s.Acquire("", "Z"), api.ErrEmpty) {
		t.Fatal("empty ref must be ErrEmpty")
	}
	if !errors.Is(s.Create("Z"), api.ErrExists) {
		t.Fatal("dup create must be ErrExists")
	}
	if !errors.Is(s.Acquire("a", "nope"), api.ErrNotFound) {
		t.Fatal("acquire missing must be ErrNotFound")
	}
	if !errors.Is(s.Release("b", "Z"), api.ErrNotHeld) {
		t.Fatal("release unheld must be ErrNotHeld")
	}
}

// TestConcurrentReads: N goroutines read RefCount/Alive concurrently
// and must agree per object. Barrier channel, no sleeps.
func TestConcurrentReads(t *testing.T) {
	s := api.New()
	const objs = 64
	for i := 0; i < objs; i++ {
		id := fmt.Sprintf("o%d", i)
		_ = s.Create(id)
		for j := 0; j <= i%3; j++ {
			_ = s.Acquire(fmt.Sprintf("r%d", j), id)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan string, 256)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < objs; i++ {
				id := fmt.Sprintf("o%d", i)
				if n, err := s.RefCount(id); err != nil || n != i%3+1 || !s.Alive(id) {
					errs <- fmt.Sprintf("%s: n=%d err=%v", id, n, err)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
}
