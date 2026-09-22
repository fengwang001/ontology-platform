package ontology

import (
	"errors"
	"math"
	"sync"
	"testing"
)

func TestConcurrentAdd(t *testing.T) {
	h, err := New(-1, 1, 8)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	const perGoroutine = 1000
	var wg sync.WaitGroup

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				x := float64((g*perGoroutine+i)%7-3) / 2
				if i%53 == 0 {
					x = math.NaN()
				}
				if err := h.Add(x); err != nil && !errors.Is(err, ErrNaNSample) {
					t.Errorf("Add(%v): %v", x, err)
					return
				}
			}
		}(g)
	}

	wg.Wait()

	const attempted = goroutines * perGoroutine
	if h.Added() != attempted {
		t.Fatalf("added = %d, want %d", h.Added(), attempted)
	}
	assertInvariant(t, h, attempted)
}
