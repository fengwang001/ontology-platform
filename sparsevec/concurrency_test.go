package sparsevec

import (
	"errors"
	"math"
	"slices"
	"sync"
	"testing"
)

// Computation must never mutate its inputs.
func TestInputsNotMutated(t *testing.T) {
	a := Vector{{2, 1.5}, {4, -3}, {9, 0}}
	b := Vector{{0, 7}, {4, 2}, {9, 1}}
	ac, bc := slices.Clone(a), slices.Clone(b)
	if _, _, err := Dot(a, b); err != nil {
		t.Fatalf("Dot: %v", err)
	}
	if _, _, err := Cosine(a, b); err != nil {
		t.Fatalf("Cosine: %v", err)
	}
	if !slices.Equal(a, ac) || !slices.Equal(b, bc) {
		t.Fatalf("inputs mutated: a=%v b=%v", a, b)
	}
}

var (
	errBits  = errors.New("bit mismatch across repeated runs")
	errSteps = errors.New("step count mismatch across goroutines")
)

// Concurrent computations on distinct pairs must be race-free and each
// goroutine must observe only its own step counts.
func TestConcurrentStepsIsolated(t *testing.T) {
	const workers = 16
	const iters = 200
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			a := Vector{
				{Index: uint32(w), Value: float64(w) + 1},
				{Index: 1_000_000_000, Value: 2},
			}
			b := Vector{
				{Index: uint32(w), Value: 3},
				{Index: 1_000_000_000, Value: 4},
			}
			first, st, err := Dot(a, b)
			if err != nil {
				errs <- err
				return
			}
			wantSteps := st.Steps
			for i := 0; i < iters; i++ {
				got, st, err := Dot(a, b)
				if err != nil {
					errs <- err
					return
				}
				if math.Float64bits(got) != math.Float64bits(first) {
					errs <- errBits
					return
				}
				if st.Steps != wantSteps || st.Steps != 2 {
					errs <- errSteps
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent run: %v", err)
	}
}
