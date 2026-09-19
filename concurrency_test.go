package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentEvalLeafCounts runs many goroutines against one shared
// Evaluator. Each goroutine uses its own Result handle and its own
// property map, and must observe a stable value and leaf count.
// Run with -race to validate data-race freedom.
func TestConcurrentEvalLeafCounts(t *testing.T) {
	ev := NewEvaluator(16)
	tree := AndP(
		Eq("a", int64(1)),
		OrP(Eq("b", int64(2)), Eq("c", int64(3))),
		NotP(IsNull("a")),
	)
	const workers = 32
	const iterations = 200

	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			r := ev.NewResult()
			props := map[string]any{
				"a": int64(1),
				"b": int64(w % 4),
				"c": int64(3),
			}
			for i := 0; i < iterations; i++ {
				v, err := ev.Eval(tree, props, r)
				if err != nil {
					errs <- "unexpected error"
					return
				}
				// b == 2 only when w%4 == 2; otherwise the Or
				// evaluates both leaves (c == 3 is True).
				wantLeaves := 3
				if w%4 == 2 {
					wantLeaves = 2
				}
				if v != True || r.LeafCount() != wantLeaves {
					errs <- "value or leaf count mismatch"
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

// TestIndependentResultsDoNotInterfere verifies that Results owned by
// different goroutines never interfere. (Sharing a single Result
// across goroutines is outside the contract.)
func TestIndependentResultsDoNotInterfere(t *testing.T) {
	ev := NewEvaluator(8)
	p := AndP(Eq("x", int64(1)), Eq("y", int64(1)))
	props := map[string]any{"x": int64(1), "y": int64(1)}

	counts := make([]int, 100)
	var wg sync.WaitGroup
	for i := range counts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := ev.NewResult()
			if _, err := ev.Eval(p, props, r); err != nil {
				t.Error(err)
			}
			counts[i] = r.LeafCount()
		}(i)
	}
	wg.Wait()
	for i, n := range counts {
		if n != 2 {
			t.Fatalf("goroutine %d saw leaf count %d, want 2", i, n)
		}
	}
}
