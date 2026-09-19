package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentEvaluationLeafCounts(t *testing.T) {
	ev := NewEvaluator(32)
	tree := AndPred{
		Left:  OrPred{Left: unknownLeaf, Right: trueLeaf},
		Right: NotPred{Child: falseLeaf},
	}
	// Whatever the attribute values, this tree evaluates exactly
	// 3 leaves: Unknown never short-circuits the Or, and the And
	// left side is never False.
	const workers = 32
	const iterations = 200

	var wg sync.WaitGroup
	errs := make(chan error, workers*iterations)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			attrs := map[string]any{"a": int64(seed)}
			for i := 0; i < iterations; i++ {
				res, err := ev.Evaluate(tree, attrs)
				if err != nil {
					errs <- err
					return
				}
				if res.Leaves != 3 {
					errs <- &leafCountError{got: res.Leaves, want: 3}
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

type leafCountError struct{ got, want int }

func (e *leafCountError) Error() string {
	return "leaf count mismatch across goroutines"
}
