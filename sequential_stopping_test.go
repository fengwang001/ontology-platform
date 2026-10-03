package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustStopper(t *testing.T, rA, rB, nmin, minStep, nmax int64, boundaries []int64, tf, tau int64) *SequentialABStopper {
	t.Helper()
	stopper, err := NewSequentialABStopper(rA, rB, nmin, minStep, nmax, boundaries, tf, tau)
	if err != nil {
		t.Fatalf("constructor failed: %v", err)
	}
	return stopper
}

func mustLook(t *testing.T, stopper *SequentialABStopper, nA, cA, nB, cB int64) LookResult {
	t.Helper()
	result, err := stopper.Look(nA, cA, nB, cB)
	if err != nil {
		t.Fatalf("Look(%d,%d,%d,%d) failed: %v", nA, cA, nB, cB, err)
	}
	return result
}

func TestRatioCheckThreshold(t *testing.T) {
	t.Run("check starts at n=2*nmin", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 100, 1, 1000, []int64{1}, 0, 0)
		if result := mustLook(t, stopper, 100, 0, 99, 0); result.Conclusion != ContinueSampleTooSmall {
			t.Fatalf("got conclusion %v", result.Conclusion)
		}

		result := mustLook(t, stopper, 101, 0, 99, 0)
		if result.Conclusion != RatioInvalid {
			t.Fatalf("got conclusion %v, want ratio invalid", result.Conclusion)
		}
		if result.EffectiveLook || result.LookIndex != 0 {
			t.Fatalf("ratio invalid look was counted: %+v", result)
		}
	})

	t.Run("deviation equal to tau passes and one more fails", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 100, 1, 1000, []int64{1}, 0, 5)
		result := mustLook(t, stopper, 210, 0, 190, 0)
		if result.Conclusion != ContinueRunning {
			t.Fatalf("exact tolerance got %v", result.Conclusion)
		}

		result = mustLook(t, stopper, 211, 0, 190, 0)
		if result.Conclusion != RatioInvalid {
			t.Fatalf("one beyond tolerance got %v", result.Conclusion)
		}
	})
}

func TestSampleAndStepThresholds(t *testing.T) {
	stopper := mustStopper(t, 1, 1, 100, 200, 100000, []int64{1}, 0, 100)

	result := mustLook(t, stopper, 99, 0, 100, 0)
	if result.Conclusion != ContinueSampleTooSmall {
		t.Fatalf("nA one below nmin got %v", result.Conclusion)
	}
	result = mustLook(t, stopper, 100, 0, 100, 0)
	if result.Conclusion != ContinueRunning || !result.EffectiveLook || result.LookIndex != 1 {
		t.Fatalf("first observation got %+v", result)
	}

	result = mustLook(t, stopper, 100, 0, 100, 0)
	if result.Conclusion != ContinueObservation || result.LookIndex != 0 {
		t.Fatalf("zero increment got %+v", result)
	}

	result = mustLook(t, stopper, 100, 0, 100, 0)
	if result.Conclusion != ContinueObservation || result.LookIndex != 0 {
		t.Fatalf("repeated non-counted look changed state: %+v", result)
	}

	result = mustLook(t, stopper, 199, 0, 200, 0)
	if result.Conclusion != ContinueObservation || result.LookIndex != 0 {
		t.Fatalf("increment one below minStep got %+v", result)
	}

	result = mustLook(t, stopper, 200, 0, 200, 0)
	if result.Conclusion != ContinueRunning || !result.EffectiveLook || result.LookIndex != 2 {
		t.Fatalf("increment equal minStep got: %+v", result)
	}

	result = mustLook(t, stopper, 200, 0, 200, 0)
	if result.Conclusion != ContinueObservation {
		t.Fatalf("zero increment after counted look got %v", result.Conclusion)
	}
}

func TestBoundarySelectionAndClamping(t *testing.T) {
	stopper := mustStopper(t, 1, 1, 1, 1, 1000, []int64{2000, 1000, 400}, 0, 100)
	expected := []int64{2000, 1000, 400, 400, 400}

	for i, boundary := range expected {
		result := mustLook(t, stopper, int64(i+1), 0, int64(i+1), 0)
		if !result.EffectiveLook || result.LookIndex != i+1 || result.Boundary != boundary {
			t.Fatalf("look %d got %+v, want boundary %d", i+1, result, boundary)
		}
	}
}

func TestStatisticThresholdAndSign(t *testing.T) {
	t.Run("equal threshold is significant winner", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 100, 200, 1000, []int64{8}, 0, 100)
		result := mustLook(t, stopper, 100, 49, 100, 51)
		if result.Conclusion != Winner || result.StatisticLeft != result.StatisticRight {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("negative D is worse", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 100, 200, 1000, []int64{8}, 0, 100)
		result := mustLook(t, stopper, 100, 51, 100, 49)
		if result.Conclusion != Worse {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("zero D continues", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 100, 200, 1000, []int64{2000}, 1, 100)
		result := mustLook(t, stopper, 100, 10, 100, 10)
		if result.Conclusion != ContinueRunning || result.D != 0 {
			t.Fatalf("got %+v", result)
		}
	})
}

func TestUndefinedStatistic(t *testing.T) {
	t.Run("all zero does not use multiplication for futility", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 1, 1, 100, []int64{1}, 0, 100)
		result := mustLook(t, stopper, 25, 0, 25, 0)
		if result.Conclusion != ContinueRunning {
			t.Fatalf("Tf=0 undefined statistic got %v", result.Conclusion)
		}

		stopper = mustStopper(t, 1, 1, 1, 1, 100, []int64{1}, 1, 100)
		result = mustLook(t, stopper, 25, 0, 25, 0)
		if result.Conclusion != Futile {
			t.Fatalf("Tf>0 at 2n=Nmax got %v", result.Conclusion)
		}
	})

	t.Run("all one same behavior", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 1, 1, 100, []int64{1}, 1, 100)
		result := mustLook(t, stopper, 25, 25, 25, 25)
		if result.Conclusion != Futile || result.D != 0 {
			t.Fatalf("got %+v", result)
		}
	})
}

func TestFutilityOrderAndThreshold(t *testing.T) {
	t.Run("nmax before futility", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 10, 1, 100, []int64{1_000_000}, 0, 100)
		result := mustLook(t, stopper, 50, 20, 50, 21)
		if result.Conclusion != Futile {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("futility only at 2n >= nmax", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 10, 1, 100, []int64{1_000_000}, 1, 100)
		result := mustLook(t, stopper, 24, 12, 24, 12)
		if result.Conclusion != ContinueRunning {
			t.Fatalf("2n one below nmax got %v", result.Conclusion)
		}

		result = mustLook(t, stopper, 25, 12, 25, 12)
		if result.Conclusion != Futile {
			t.Fatalf("2n equal nmax got %+v", result)
		}
	})
}

func TestLargeProductBeyondInt64(t *testing.T) {
	stopper := mustStopper(t, 1, 1, 1, 1, 2_000_000, []int64{5000}, 0, 100)
	result := mustLook(t, stopper, 1_000_000, 475_000, 1_000_000, 525_000)
	if result.Conclusion != Winner {
		t.Fatalf("large product decision got %+v", result)
	}
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	t.Run("invalid constructor parameters", func(t *testing.T) {
		valid := []int64{1}
		cases := []struct {
			name       string
			rA, rB     int64
			nmin       int64
			minStep    int64
			nmax       int64
			boundaries []int64
			tf, tau    int64
		}{
			{"rA zero", 0, 1, 1, 1, 2, valid, 0, 0},
			{"rB too large", 1, 101, 1, 1, 2, valid, 0, 0},
			{"nmin zero", 1, 1, 0, 1, 2, valid, 0, 0},
			{"minStep too large", 1, 1, 1, 1_000_001, 2, valid, 0, 0},
			{"nmax one", 1, 1, 1, 1, 1, valid, 0, 0},
			{"no boundaries", 1, 1, 1, 1, 2, nil, 0, 0},
			{"boundary zero", 1, 1, 1, 1, 2, []int64{0}, 0, 0},
			{"too many boundaries", 1, 1, 1, 1, 2, make([]int64, 9), 0, 0},
			{"tf negative", 1, 1, 1, 1, 2, valid, -1, 0},
			{"tau too large", 1, 1, 1, 1, 2, valid, 0, 101},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := NewSequentialABStopper(tc.rA, tc.rB, tc.nmin, tc.minStep, tc.nmax, tc.boundaries, tc.tf, tc.tau)
				if !errors.Is(err, ErrInvalidArguments) {
					t.Fatalf("got %v, want ErrInvalidArguments", err)
				}
			})
		}
	})

	t.Run("invalid arguments ordered before stopped", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 1, 1, 100, []int64{1}, 0, 0)
		_, err := stopper.Look(1, 2, 1, 0)
		if !errors.Is(err, ErrInvalidArguments) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("look rejected after stop", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 1, 1, 100, []int64{1_000_000}, 0, 0)
		mustLook(t, stopper, 50, 50, 50, 50)
		before := stopper.Status()
		_, err := stopper.Look(51, 51, 51, 51)
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("got %v", err)
		}
		if got := stopper.Status(); got != before {
			t.Fatalf("status changed: %+v vs %+v", got, before)
		}
	})

	t.Run("regression rejected and state preserved", func(t *testing.T) {
		stopper := mustStopper(t, 1, 1, 1, 1, 1000, []int64{1}, 0, 100)
		mustLook(t, stopper, 10, 1, 10, 1)
		before := stopper.Status()
		_, err := stopper.Look(9, 1, 10, 1)
		if !errors.Is(err, ErrDataRegression) {
			t.Fatalf("got %v", err)
		}
		if got := stopper.Status(); got != before {
			t.Fatalf("status changed: %+v vs %+v", got, before)
		}

		mustLook(t, stopper, 10, 1, 10, 1)
		if got := stopper.Status(); got.EffectiveLooks != before.EffectiveLooks ||
			got.LastCountedN != before.LastCountedN || got.State != before.State {
			t.Fatalf("accepted repeat changed countable state: %+v vs %+v", got, before)
		}
	})
}

func TestConcurrentStatusAndLook(t *testing.T) {
	stopper := mustStopper(t, 1, 1, 1, 1, 1000, []int64{1_000_000}, 0, 100)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_, _ = stopper.Look(int64(10+i), 1, int64(10+i), 1)
		}(i)
		go func() {
			defer wg.Done()
			_ = stopper.Status()
		}()
	}

	wg.Wait()
}
