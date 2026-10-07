package graph

import (
	"testing"
)

func TestResultCounterReachedIsConstantSpaceAcrossLimits(t *testing.T) {
	for _, limit := range []int{1, 10, 1_000, 100_000} {
		counter := newResultCounter(limit)
		for i := 0; i < limit; i++ {
			counter.acceptOne()
		}

		if testing.AllocsPerRun(1000, func() {
			if !counter.reached() {
				t.Fatalf("limit=%d counter did not reach", limit)
			}
		}) != 0 {
			t.Fatalf("limit=%d reached() allocated memory", limit)
		}
	}
}
