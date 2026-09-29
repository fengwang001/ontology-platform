package quantile

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

func TestCompressesSmallestAdjacentCountSum(t *testing.T) {
	maintainer, err := New(3)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	values := []float64{1, 2, 3, 4, 5}
	for _, value := range values {
		if err := maintainer.Add(value); err != nil {
			t.Fatalf("Add(%v) returned error: %v", value, err)
		}
		logState(t, fmt.Sprintf("Add(%v)", value), maintainer, "after insertion; ties resolve to the leftmost adjacent pair")
	}

	got := maintainer.Snapshot()
	want := []Centroid{
		{Mean: 1.5, Count: 2, Min: 1, Max: 2},
		{Mean: 3.5, Count: 2, Min: 3, Max: 4},
		{Mean: 5, Count: 1, Min: 5, Max: 5},
	}
	assertCentroids(t, got, want)
	logState(t, "compress-smallest-pair", maintainer, "pair counts before adding 5 were [3,2], so 3 and 4 were merged")
}

func TestQuantileLinearInterpolation(t *testing.T) {
	maintainer, err := New(2)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	for _, value := range []float64{1, 2, 3, 4} {
		if err := maintainer.Add(value); err != nil {
			t.Fatalf("Add(%v) returned error: %v", value, err)
		}
	}

	got, err := maintainer.Quantile(0.5)
	if err != nil {
		t.Fatalf("Quantile returned error: %v", err)
	}
	const want = 2.5
	if got != want {
		t.Fatalf("Quantile(0.5) = %v, want %v", got, want)
	}
	logState(t, "Quantile(0.5)", maintainer, "zero-based target rank 1.5 interpolates halfway between means 1.5 and 3.5")
}

func TestWithdrawRebuildsFromRetainedValues(t *testing.T) {
	maintainer, err := New(2)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	for _, value := range []float64{1, 2, 3, 100} {
		if err := maintainer.Add(value); err != nil {
			t.Fatalf("Add(%v) returned error: %v", value, err)
		}
	}
	logState(t, "before Withdraw(100)", maintainer, "old compressed centroids must not be incrementally edited")

	if err := maintainer.Withdraw(100); err != nil {
		t.Fatalf("Withdraw returned error: %v", err)
	}
	got := maintainer.Snapshot()
	want := []Centroid{
		{Mean: 1.5, Count: 2, Min: 1, Max: 2},
		{Mean: 3, Count: 1, Min: 3, Max: 3},
	}
	assertCentroids(t, got, want)
	if count := maintainer.Count(); count != 3 {
		t.Fatalf("Count after withdraw = %d, want 3", count)
	}
	if err := maintainer.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck returned error: %v", err)
	}
	logState(t, "Withdraw(100)", maintainer, "remaining sorted values [1 2 3] were rebuilt with budget 2")
}

func TestMergeCountsAndKeepsOtherUnchanged(t *testing.T) {
	left, err := New(2)
	if err != nil {
		t.Fatalf("New left returned error: %v", err)
	}
	right, err := New(2)
	if err != nil {
		t.Fatalf("New right returned error: %v", err)
	}
	for _, value := range []float64{1, 2, 3} {
		if err := left.Add(value); err != nil {
			t.Fatalf("left.Add(%v) returned error: %v", value, err)
		}
	}
	for _, value := range []float64{10, 20} {
		if err := right.Add(value); err != nil {
			t.Fatalf("right.Add(%v) returned error: %v", value, err)
		}
	}
	rightBefore := right.Snapshot()

	if err := left.Merge(right); err != nil {
		t.Fatalf("Merge returned error: %v", err)
	}
	if got, want := left.Count(), 5; got != want {
		t.Fatalf("merged Count = %d, want %d", got, want)
	}
	assertCentroids(t, right.Snapshot(), rightBefore)
	if err := left.SelfCheck(); err != nil {
		t.Fatalf("merged SelfCheck returned error: %v", err)
	}
	mergedCentroids := left.Snapshot()
	if mergedCentroids[0].Count != 2 || mergedCentroids[1].Count != 3 {
		t.Fatalf("merged centroid counts = [%d %d], want [2 3]", mergedCentroids[0].Count, mergedCentroids[1].Count)
	}
	logState(t, "Merge(right)", left, "count 3+2=5; combined ordered centroids were compressed to budget 2")
	logState(t, "right after Merge", right, "snapshot is compared byte-for-value/count with pre-merge snapshot")
}

func TestEmptySelfCheckAndCount(t *testing.T) {
	maintainer, err := New(1)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if count := maintainer.Count(); count != 0 {
		t.Fatalf("empty Count = %d, want 0", count)
	}
	if len(maintainer.Snapshot()) != 0 {
		t.Fatalf("empty Snapshot = %v, want no centroids", maintainer.Snapshot())
	}
	if err := maintainer.SelfCheck(); err != nil {
		t.Fatalf("empty SelfCheck returned error: %v", err)
	}
	logState(t, "empty invariants", maintainer, "zero count and zero centroids are valid until a quantile is queried")
}

func TestInvalidOperationsDoNotMutateState(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidBudget) {
		t.Fatalf("New(0) error = %v, want %v", err, ErrInvalidBudget)
	}

	maintainer, err := New(2)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if _, err := maintainer.Quantile(0.5); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty Quantile error = %v, want %v", err, ErrEmpty)
	}
	if err := maintainer.Add(1); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	tests := []struct {
		name      string
		operation func() error
		want      error
	}{
		{"add NaN", func() error { return maintainer.Add(math.NaN()) }, ErrInvalidValue},
		{"quantile below zero", func() error { _, err := maintainer.Quantile(-0.1); return err }, ErrInvalidQuantile},
		{"quantile above one", func() error { _, err := maintainer.Quantile(1.1); return err }, ErrInvalidQuantile},
		{"quantile NaN", func() error { _, err := maintainer.Quantile(math.NaN()); return err }, ErrInvalidQuantile},
		{"withdraw missing", func() error { return maintainer.Withdraw(2) }, ErrValueNotFound},
		{"withdraw NaN", func() error { return maintainer.Withdraw(math.NaN()) }, ErrInvalidValue},
		{"merge nil", func() error { return maintainer.Merge(nil) }, ErrNilMaintainer},
		{"merge self", func() error { return maintainer.Merge(maintainer) }, ErrMergeSelf},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := maintainer.Snapshot()
			beforeCount := maintainer.Count()
			err := tt.operation()
			if !errors.Is(err, tt.want) {
				t.Fatalf("%s error = %v, want %v", tt.name, err, tt.want)
			}
			assertCentroids(t, maintainer.Snapshot(), before)
			if count := maintainer.Count(); count != beforeCount {
				t.Fatalf("%s changed count from %d to %d", tt.name, beforeCount, count)
			}
			logState(t, tt.name+" rejected", maintainer, "centroids and retained count are compared with pre-operation state")
		})
	}
}

func TestConcurrentReadersAreDeterministicAndSelfCheckSafe(t *testing.T) {
	maintainer, err := New(3)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	for _, value := range []float64{1, 2, 3, 4, 5, 6, 7} {
		if err := maintainer.Add(value); err != nil {
			t.Fatalf("Add(%v) returned error: %v", value, err)
		}
	}

	const readerCount = 32
	results := make([]uint64, readerCount)
	var wg sync.WaitGroup
	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			value, qErr := maintainer.Quantile(0.618)
			if qErr != nil {
				t.Errorf("Quantile returned error: %v", qErr)
				return
			}
			results[index] = math.Float64bits(value)
			if cErr := maintainer.SelfCheck(); cErr != nil {
				t.Errorf("SelfCheck returned error: %v", cErr)
			}
		}(i)
	}
	wg.Wait()
	for i := 1; i < readerCount; i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent quantiles differed: %016x vs %016x", results[0], results[i])
		}
	}
	if count := maintainer.Count(); count != 7 {
		t.Fatalf("Count = %d, want 7", count)
	}
	logState(t, "concurrent Quantile(0.618)/Count/SelfCheck", maintainer, "32 readers compare raw Float64bits and all self-checks pass")
}

func TestEstimateIsWithinReportedErrorBound(t *testing.T) {
	maintainer, err := New(3)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	values := []float64{1, 2, 3, 4, 10, 20, 21, 22, 100}
	for _, value := range values {
		if err := maintainer.Add(value); err != nil {
			t.Fatalf("Add(%v) returned error: %v", value, err)
		}
	}

	for q := 0.0; q <= 1.0; q += 0.05 {
		estimate, err := maintainer.Quantile(q)
		if err != nil {
			t.Fatalf("Quantile(%v) returned error: %v", q, err)
		}
		exact, err := maintainer.ExactQuantile(q)
		if err != nil {
			t.Fatalf("ExactQuantile(%v) returned error: %v", q, err)
		}
		bound, err := maintainer.ErrorBound(q)
		if err != nil {
			t.Fatalf("ErrorBound(%v) returned error: %v", q, err)
		}
		if math.Abs(estimate-exact) > bound {
			t.Fatalf("q=%v estimate=%v exact=%v difference=%v exceeds bound=%v", q, estimate, exact, math.Abs(estimate-exact), bound)
		}
	}
	logState(t, "error-bound sweep", maintainer, "exact retained-set quantiles verify |estimate-exact| <= ErrorBound for q from 0 to 1")
}

func assertCentroids(t *testing.T, got, want []Centroid) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("centroid count = %d, want %d; got %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("centroid[%d] = %#v, want %#v; all got = %#v", i, got[i], want[i], got)
		}
	}
}

func logState(t *testing.T, operation string, maintainer *Maintainer, basis string) {
	t.Helper()
	t.Logf("operation=%q centroids=%v total=%d basis=%q", operation, maintainer.Snapshot(), maintainer.Count(), basis)
}
