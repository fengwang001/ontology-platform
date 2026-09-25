package sparse

import (
	"math"
	"sync"
	"testing"
)

// TestConcurrentStatsIsolation runs many goroutines on distinct vector
// pairs, each with a known step count, and verifies results and stats
// never leak across goroutines. Run with -race.
func TestConcurrentStatsIsolation(t *testing.T) {
	const workers = 32
	const iters = 200
	var wg sync.WaitGroup
	errCh := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each worker owns a pair whose merge takes exactly
			// w%7+2 steps and whose dot is (w%5)+1.
			matches := uint32(w%5) + 1
			a := &Vector{}
			b := &Vector{}
			for k := uint32(0); k < matches; k++ {
				a.Elems = append(a.Elems, Element{Index: k * 2, Value: 1})
				b.Elems = append(b.Elems, Element{Index: k * 2, Value: 1})
			}
			// Pad with non-overlapping tails to vary step counts.
			pad := w % 7
			for p := 0; p < pad; p++ {
				a.Elems = append(a.Elems, Element{Index: 1000 + 2*uint32(p), Value: 1})
				b.Elems = append(b.Elems, Element{Index: 1001 + 2*uint32(p), Value: 1})
			}
			wantSteps := simulateSteps(a.Elems, b.Elems)
			wantDot := float64(matches)
			for i := 0; i < iters; i++ {
				dot, stats, err := Dot(a, b)
				if err != nil {
					errCh <- err.Error()
					return
				}
				if stats.Steps != wantSteps {
					errCh <- "step count leaked across goroutines"
					return
				}
				if math.Float64bits(dot) != math.Float64bits(wantDot) {
					errCh <- "dot result not bit-identical across iterations"
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
}

// TestConcurrentSharedInputsReadOnly hammers the same two vectors from
// many goroutines; safe because inputs are never mutated.
func TestConcurrentSharedInputsReadOnly(t *testing.T) {
	a := &Vector{Elems: []Element{
		{Index: 0, Value: 1e16}, {Index: 3, Value: 1}, {Index: 6, Value: -1e16},
	}}
	b := &Vector{Elems: []Element{
		{Index: 0, Value: 2}, {Index: 3, Value: 5}, {Index: 6, Value: 1},
	}}
	want, wantStats, err := Dot(a, b)
	if err != nil {
		t.Fatalf("Dot: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				dot, stats, err := Dot(a, b)
				if err != nil {
					t.Error(err)
					return
				}
				if math.Float64bits(dot) != math.Float64bits(want) ||
					stats != wantStats {
					t.Error("concurrent result diverged")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// simulateSteps counts merge iterations the same way mergeDot does,
// giving each worker an independent expectation.
func simulateSteps(a, b []Element) uint64 {
	var steps uint64
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			i++
			j++
		}
	}
	return steps
}
