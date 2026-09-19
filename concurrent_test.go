package coercion

import (
	"sync"
	"testing"
)

func TestConcurrentConversionsDoNotMixDegradations(t *testing.T) {
	converter := New(Lenient)
	const goroutines = 100
	var wait sync.WaitGroup
	wait.Add(goroutines)
	errs := make(chan error, goroutines)

	for worker := 0; worker < goroutines; worker++ {
		go func(worker int) {
			defer wait.Done()
			input := []any{float64(worker) + 0.25}
			result, err := converter.Convert(input, Target{Kind: Int64Slice})
			if err != nil {
				errs <- err
				return
			}
			if len(result.Degradations) != 1 {
				errs <- mismatchError(worker, len(result.Degradations))
				return
			}
			record := result.Degradations[0]
			if record.Index != 0 || record.Category != PrecisionLoss {
				errs <- mismatchError(worker, record.Index)
			}
			if got := result.Value.([]int64); len(got) != 1 || got[0] != int64(worker) {
				errs <- mismatchError(worker, got)
			}

			// Mutating one call's result must not change a fresh call.
			again, err := converter.Convert(input, Target{Kind: Int64Slice})
			if err != nil || len(again.Degradations) != 1 {
				errs <- mismatchError(worker, again.Degradations)
			}
		}(worker)
	}

	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestResultSlicesDoNotShareBackingArrays(t *testing.T) {
	converter := New(Strict)
	first, err := converter.Convert([]any{int64(1)}, Target{Kind: Int64Slice})
	if err != nil {
		t.Fatal(err)
	}
	second, err := converter.Convert([]any{int64(2)}, Target{Kind: Int64Slice})
	if err != nil {
		t.Fatal(err)
	}
	first.Value.([]int64)[0] = 99
	if second.Value.([]int64)[0] != 2 {
		t.Fatalf("slice backing array was shared: %v", second.Value)
	}
}
