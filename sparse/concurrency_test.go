package sparse

import (
	"math"
	"sync"
	"testing"
)

// TestConcurrentDotIsolation runs many goroutines over distinct
// vector pairs. Each pair must yield bitwise-stable results and its
// own step count, with no cross-talk between goroutines.
func TestConcurrentDotIsolation(t *testing.T) {
	const workers = 16
	const iters = 200

	pairs := make([][2]Vector, workers)
	wantDot := make([]float64, workers)
	wantSteps := make([]int, workers)
	for w := 0; w < workers; w++ {
		a := Vector{
			{uint32(w), float64(w) + 1},
			{uint32(1000 + w), -0.5},
			{uint32(1_000_000_000 - w), 2},
		}
		b := Vector{
			{uint32(w), 3},
			{uint32(500 + w), 1.25},
			{uint32(1_000_000_000 - w), -4},
		}
		d, st, err := Dot(a, b)
		if err != nil {
			t.Fatalf("setup Dot: %v", err)
		}
		pairs[w] = [2]Vector{a, b}
		wantDot[w] = d
		wantSteps[w] = st.Steps
	}

	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			a, b := pairs[id][0], pairs[id][1]
			for i := 0; i < iters; i++ {
				d, st, err := Dot(a, b)
				if err != nil {
					errs <- err.Error()
					return
				}
				if math.Float64bits(d) != math.Float64bits(wantDot[id]) {
					errs <- "dot result drifted"
					return
				}
				if st.Steps != wantSteps[id] {
					errs <- "step count leaked across goroutines"
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
}

// TestConcurrentCosineBitwiseStable repeats cosine computations of
// the same pair from many goroutines; all results must be identical.
func TestConcurrentCosineBitwiseStable(t *testing.T) {
	a := Vector{{0, 0.1}, {3, 0.3}, {9, -0.7}, {12, 1e8}}
	b := Vector{{0, 0.2}, {3, -0.1}, {9, 0.5}, {15, 4}}

	want, _, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	var wg sync.WaitGroup
	bad := make(chan float64, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, _, err := Cosine(a, b)
				if err != nil {
					t.Errorf("Cosine: %v", err)
					return
				}
				if math.Float64bits(got) != math.Float64bits(want) {
					bad <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	for got := range bad {
		t.Fatalf("cosine drifted: %v != %v", got, want)
	}
}
