package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// Concurrent Add must not lose elements, must not duplicate any
// element, and must stay within capacity. Run with -race.
func TestConcurrentAddNoLossNoDuplicates(t *testing.T) {
	const goroutines = 8
	const perGoroutine = 5000
	const k = 100

	r, err := New(k, 23)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				name := fmt.Sprintf("g%d-item%d", g, i)
				if err := r.Add(name, 1+float64(i%5)); err != nil {
					t.Errorf("Add failed: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if got, want := r.Total(), uint64(goroutines*perGoroutine); got != want {
		t.Fatalf("Total() = %d, want %d (lost elements)", got, want)
	}
	if r.Len() != k {
		t.Fatalf("Len() = %d, want %d", r.Len(), k)
	}
	seen := map[string]bool{}
	for _, s := range r.Sample() {
		if seen[s] {
			t.Fatalf("duplicate element in sample: %q", s)
		}
		seen[s] = true
	}
}

// Sample must be safe to call concurrently with Add.
func TestConcurrentAddAndSample(t *testing.T) {
	r, err := New(64, 29)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				if err := r.Add(fmt.Sprintf("w%d-%d", g, i), 1); err != nil {
					t.Errorf("Add failed: %v", err)
					return
				}
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				s := r.Sample()
				if len(s) > 64 {
					t.Errorf("sample larger than capacity: %d", len(s))
					return
				}
			}
		}()
	}
	wg.Wait()
	if got, want := r.Total(), uint64(4*2000); got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
	}
}
