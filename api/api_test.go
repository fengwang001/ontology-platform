package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

// TestSentinelErrors: each fault injection maps to its own sentinel, and the
// three sentinels are mutually distinct.
func TestSentinelErrors(t *testing.T) {
	if _, err := api.New(nil); !errors.Is(err, api.ErrInvalidConfig) {
		t.Fatalf("empty weights: %v", err)
	}
	if _, err := api.New([]int{2, 0, 1}); !errors.Is(err, api.ErrInvalidConfig) {
		t.Fatalf("non-positive weight: %v", err)
	}
	if _, err := api.New([]int{-3}); !errors.Is(err, api.ErrInvalidConfig) {
		t.Fatalf("negative weight: %v", err)
	}
	b, err := api.New([]int{3, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{-1, 3, 100} {
		if err := b.SetWeight(i, 5); !errors.Is(err, api.ErrIndexOutOfRange) {
			t.Fatalf("index %d: %v", i, err)
		}
	}
	for _, w := range []int{0, -7} {
		if err := b.SetWeight(0, w); !errors.Is(err, api.ErrInvalidWeight) {
			t.Fatalf("weight %d: %v", w, err)
		}
	}
	all := []error{api.ErrInvalidConfig, api.ErrIndexOutOfRange, api.ErrInvalidWeight}
	for i, x := range all {
		for j, y := range all {
			if i != j && errors.Is(x, y) {
				t.Fatalf("sentinels %d and %d are not distinct", i, j)
			}
		}
	}
}

// TestRejectedKeepsState: rejected calls change nothing observable.
func TestRejectedKeepsState(t *testing.T) {
	b, _ := api.New([]int{3, 1, 2})
	ctrl, _ := api.New([]int{3, 1, 2})
	b.Next()
	ctrl.Next()
	_ = b.SetWeight(9, 9)
	_ = b.SetWeight(-2, 1)
	_ = b.SetWeight(0, 0)
	_ = b.SetWeight(1, -4)
	for i, w := range []int{3, 1, 2} {
		if b.Weight(i) != w {
			t.Fatalf("weight %d changed to %d", i, b.Weight(i))
		}
	}
	for k := 0; k < 12; k++ {
		if x, y := b.Next(), ctrl.Next(); x != y {
			t.Fatalf("sequence diverged at %d: %d vs %d", k, x, y)
		}
	}
	// a valid SetWeight still works afterwards
	if err := b.SetWeight(1, 5); err != nil {
		t.Fatal(err)
	}
	if b.Weight(1) != 5 {
		t.Fatal("valid SetWeight rejected")
	}
}

// TestConcurrentFairness: many goroutines calling Next concurrently end with
// counts exactly proportional to the weights. No sleeps.
func TestConcurrentFairness(t *testing.T) {
	w := []int{3, 1, 2}
	b, err := api.New(w)
	if err != nil {
		t.Fatal(err)
	}
	const G, per = 8, 300 // 2400 picks = 400 full cycles of W=6
	counts := make([]int, len(w))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]int, len(w))
			for k := 0; k < per; k++ {
				local[b.Next()]++
			}
			mu.Lock()
			for i := range counts {
				counts[i] += local[i]
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	total := G * per
	for i := range w {
		want := total * w[i] / 6
		if counts[i] != want {
			t.Fatalf("server %d picked %d times, want %d", i, counts[i], want)
		}
	}
}

// TestSelfCheck: the built-in self-check passes on a healthy balancer and is
// safe to call concurrently with Next.
func TestSelfCheck(t *testing.T) {
	b, err := api.New([]int{3, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				b.Next()
				_ = b.Weight(0)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := b.SelfCheck(); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
}
