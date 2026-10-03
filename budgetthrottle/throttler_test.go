package budgetthrottle

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, budget int64, periods int, length int64, weights []int64, catchUp int64) *Throttler {
	t.Helper()
	throttler, err := NewThrottler(budget, periods, length, weights, catchUp)
	if err != nil {
		t.Fatalf("invalid constructor: %v", err)
	}
	return throttler
}

func TestExampleFromSpecification(t *testing.T) {
	throttler := mustNew(t, 1000, 4, 10, []int64{1, 3, 1, 5}, 50)
	if err := throttler.Try(60, 3); err != nil {
		t.Fatalf("Try(60,3): %v", err)
	}
	if err := throttler.Try(50, 5); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Try(50,5)=%v, want rate limited", err)
	}
	if err := throttler.Try(130, 6); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(130,6)=%v, want amount too large", err)
	}
	allowance, err := throttler.Allowance(25)
	if err != nil || allowance != 150 {
		t.Fatalf("Allowance(25)=(%d,%v), want 150", allowance, err)
	}
	if err := throttler.Try(150, 25); err != nil {
		t.Fatalf("Try(150,25): %v", err)
	}
	if err := throttler.Refund(30, 26); err != nil {
		t.Fatalf("Refund(30,26): %v", err)
	}
	if err := throttler.Try(30, 27); err != nil {
		t.Fatalf("Try(30,27): %v", err)
	}
	if throttler.spent != 210 || throttler.periodSpent != 150 {
		t.Fatalf("state=(spent=%d ps=%d), want (210,150) after refund and re-spend", throttler.spent, throttler.periodSpent)
	}
}

func TestTryBoundaryAmounts(t *testing.T) {
	throttler := mustNew(t, 100, 1, 10, []int64{1}, 0)
	if err := throttler.Try(50, 0); err != nil {
		t.Fatalf("Try(50,0): %v", err)
	}
	if err := throttler.Try(50, 1); err != nil {
		t.Fatalf("Try(50,1): %v; want accepted when ps+a==A", err)
	}
	if err := throttler.Try(1, 2); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("Try(1,2)=%v, want budget exhausted", err)
	}

	throttler = mustNew(t, 101, 1, 10, []int64{1}, 0)
	if err := throttler.Try(101, 0); err != nil {
		t.Fatalf("Try(101,0): %v; want accepted when a==A", err)
	}

	throttler = mustNew(t, 102, 2, 10, []int64{1, 1}, 0)
	if err := throttler.Try(102, 0); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(102,0)=%v, want amount too large for A0=51", err)
	}

	throttler = mustNew(t, 100, 2, 10, []int64{1, 1}, 0)
	if err := throttler.Try(49, 0); err != nil {
		t.Fatalf("Try(49,0): %v", err)
	}
	if err := throttler.Try(2, 1); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Try(2,1)=%v, want rate limited one over remaining", err)
	}
}

func TestZeroPeriodBudgetAndCatchUp(t *testing.T) {
	throttler := mustNew(t, 3, 4, 10, []int64{1, 1, 1, 1}, 0)
	if got := throttler.targets; got[0] != 0 || got[1] != 1 || got[2] != 2 || got[3] != 3 {
		t.Fatalf("targets=%v, want [0 1 2 3]", got)
	}
	if allowance, _ := throttler.Allowance(0); allowance != 0 {
		t.Fatalf("Allowance(0)=%d, want q0=0", allowance)
	}
	if err := throttler.Try(1, 0); !errors.Is(err, ErrAmountTooLarge) {
		t.Fatalf("Try(1,0)=%v, want amount larger than zero allowance", err)
	}
	if charged, err := throttler.TryUpTo(1, 0); !errors.Is(err, ErrRateLimited) || charged != 0 {
		t.Fatalf("TryUpTo(1,0)=(%d,%v), want rate limited at zero remaining", charged, err)
	}

	throttler = mustNew(t, 1000, 4, 10, []int64{1, 1, 1, 1}, 100)
	if err := throttler.Try(80, 0); err != nil {
		t.Fatalf("Try(80,0): %v", err)
	}
	if allowance, _ := throttler.Allowance(10); allowance != 420 {
		t.Fatalf("allowance=%d, want 420 when deficit below cap", allowance)
	}

	throttler = mustNew(t, 1000, 4, 10, []int64{1, 1, 1, 1}, 10)
	if err := throttler.Try(80, 0); err != nil {
		t.Fatalf("Try(80,0): %v", err)
	}
	if allowance, _ := throttler.Allowance(10); allowance != 275 {
		t.Fatalf("allowance=%d, want 275 when deficit above cap", allowance)
	}
	if allowance, _ := throttler.Allowance(20); allowance != 275 {
		t.Fatalf("skipped allowance=%d, want 275 from accumulated deficit with local cap", allowance)
	}
}

func TestRemainingBudgetClamp(t *testing.T) {
	throttler := mustNew(t, 100, 2, 10, []int64{1, 1}, 10000)
	if err := throttler.Try(40, 0); err != nil {
		t.Fatalf("Try(40,0): %v", err)
	}
	allowance, err := throttler.Allowance(10)
	if err != nil || allowance != 60 {
		t.Fatalf("Allowance(10)=(%d,%v), want B-spent clamp 60", allowance, err)
	}
}

func TestRefundEffects(t *testing.T) {
	throttler := mustNew(t, 1000, 4, 10, []int64{1, 3, 1, 5}, 50)
	if err := throttler.Try(150, 25); err != nil {
		t.Fatalf("Try(150,25): %v", err)
	}
	if err := throttler.Refund(30, 26); err != nil {
		t.Fatalf("Refund(30,26): %v", err)
	}
	if allowance, _ := throttler.Allowance(27); allowance != 30 {
		t.Fatalf("current allowance=%d, want refunded headroom 30", allowance)
	}
	if allowance, _ := throttler.Allowance(30); allowance != 750 {
		t.Fatalf("next allowance=%d, want 500+min(380,250)=750", allowance)
	}

	throttler = mustNew(t, 1000, 4, 10, []int64{1, 1, 1, 1}, 100)
	if allowanceBefore, _ := throttler.Allowance(10); allowanceBefore != 500 {
		t.Fatalf("A1 with no spending=%d, want 500", allowanceBefore)
	}
	if err := throttler.Try(200, 0); err != nil {
		t.Fatalf("Try(200,0): %v", err)
	}
	if allowanceBefore, _ := throttler.Allowance(10); allowanceBefore != 300 {
		t.Fatalf("A1 just before refund=%d, want 300", allowanceBefore)
	}
	if err := throttler.Refund(200, 10); err != nil {
		t.Fatalf("Refund(200,10): %v", err)
	}
	if allowance, _ := throttler.Allowance(10); allowance != 300 {
		t.Fatalf("A1 after first-op refund=%d, want fixed pre-refund A1=300", allowance)
	}
	if allowance, _ := throttler.Allowance(20); allowance != 500 {
		t.Fatalf("A2 after refund=%d, want larger deficit reflected next period", allowance)
	}
	if err := throttler.Refund(1, 21); !errors.Is(err, ErrRefundTooLarge) {
		t.Fatalf("Refund(1,21)=%v, want refund too large", err)
	}
}

func TestTryUpTo(t *testing.T) {
	throttler := mustNew(t, 100, 2, 10, []int64{1, 1}, 0)
	charged, err := throttler.TryUpTo(30, 0)
	if err != nil || charged != 30 {
		t.Fatalf("TryUpTo(30,0)=(%d,%v), want 30", charged, err)
	}
	charged, err = throttler.TryUpTo(30, 1)
	if err != nil || charged != 20 {
		t.Fatalf("TryUpTo(30,1)=(%d,%v), want min(30,20)=20", charged, err)
	}
	charged, err = throttler.TryUpTo(30, 2)
	if !errors.Is(err, ErrRateLimited) || charged != 0 {
		t.Fatalf("TryUpTo(30,2)=(%d,%v), want rate limited at zero remaining", charged, err)
	}
}

func TestBoundariesAndPastAllowance(t *testing.T) {
	throttler := mustNew(t, 100, 2, 10, []int64{1, 1}, 0)
	if err := throttler.Try(1, 9); err != nil {
		t.Fatalf("Try(1,9): %v", err)
	}
	if err := throttler.Try(1, 10); err != nil {
		t.Fatalf("Try(1,10): %v", err)
	}
	allowance, err := throttler.Allowance(8)
	if err != nil || allowance != 0 {
		t.Fatalf("Allowance(8)=(%d,%v), want past-period zero", allowance, err)
	}
	if _, err := throttler.Allowance(20); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Allowance(20)=%v, want invalid now=nL", err)
	}
	if err := throttler.Try(1, 20); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Try(1,20)=%v, want invalid before clock", err)
	}
	if err := throttler.Try(1, 9); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("Try(1,9)=%v, want clock rollback", err)
	}
	if throttler.maxNow != 10 || throttler.spent != 2 {
		t.Fatalf("state after rejects=(maxNow=%d spent=%d), want (10,2)", throttler.maxNow, throttler.spent)
	}
}

func TestRejectionPriorityAndState(t *testing.T) {
	throttler := mustNew(t, 10, 1, 10, []int64{1}, 0)
	if err := throttler.Try(10, 0); err != nil {
		t.Fatalf("Try(10,0): %v", err)
	}
	tests := []struct {
		name string
		call func() error
		want error
	}{
		{"invalid argument before clock", func() error { return throttler.Try(0, -1) }, ErrInvalidArgument},
		{"clock before budget", func() error { return throttler.Try(1, -1) }, ErrInvalidArgument},
		{"budget before size", func() error { return throttler.Try(11, 1) }, ErrBudgetExhausted},
		{"size before rate", func() error {
			other := mustNew(t, 102, 2, 10, []int64{1, 1}, 0)
			return other.Try(101, 0)
		}, ErrAmountTooLarge},
		{"rate limited", func() error {
			other := mustNew(t, 100, 2, 10, []int64{1, 1}, 0)
			if err := other.Try(49, 0); err != nil {
				return err
			}
			return other.Try(2, 1)
		}, ErrRateLimited},
		{"refund too large", func() error {
			other := mustNew(t, 10, 1, 10, []int64{1}, 0)
			return other.Refund(1, 0)
		}, ErrRefundTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			t.Logf("input=%s output=%v reason=%s", tt.name, err, tt.want)
			if !errors.Is(err, tt.want) {
				t.Fatalf("output=%v, want %v", err, tt.want)
			}
		})
	}
	if throttler.spent != 10 || throttler.periodSpent != 10 || throttler.maxNow != 0 {
		t.Fatalf("rejected calls changed state: spent=%d ps=%d maxNow=%d", throttler.spent, throttler.periodSpent, throttler.maxNow)
	}
}

func TestConstructorValidation(t *testing.T) {
	valid := func(budget int64, periods int, length int64, weights []int64, catchUp int64) bool {
		_, err := NewThrottler(budget, periods, length, weights, catchUp)
		return err == nil
	}
	if valid(1_000_000_000_001, 1, 1, []int64{1}, 0) ||
		valid(0, 1, 1, []int64{1}, 0) ||
		valid(1, 0, 1, nil, 0) ||
		valid(1, 1441, 1, make([]int64, 1441), 0) ||
		valid(1, 1, 0, []int64{1}, 0) ||
		valid(1, 1, 1_000_001, []int64{1}, 0) ||
		valid(1, 1, 1, []int64{0}, 0) ||
		valid(1, 1, 1, []int64{1}, -1) ||
		valid(1, 1, 1, []int64{1}, 10001) ||
		valid(1, 2, 1, []int64{1}, 0) {
		t.Fatal("invalid constructor arguments were accepted")
	}
}

func TestLargeBudgetUsesExact128BitCurve(t *testing.T) {
	weights := make([]int64, 1440)
	for i := range weights {
		weights[i] = 1_000_000
	}
	throttler := mustNew(t, 1_000_000_000_000, 1440, 1_000_000, weights, 10000)
	for i, target := range throttler.targets {
		want := int64(i+1) * 1_000_000_000_000 / 1440
		if target != want {
			t.Fatalf("target[%d]=%d, want %d from B*W intermediate product", i, target, want)
		}
	}
}

func TestAllowanceDoesNotTraverseTargets(t *testing.T) {
	for _, periods := range []int{100, 1440} {
		t.Run(fmt.Sprintf("periods=%d", periods), func(t *testing.T) {
			weights := make([]int64, periods)
			for i := range weights {
				weights[i] = 1
			}
			throttler := mustNew(t, 1_000_000_000_000, periods, 1, weights, 1234)
			period := periods - 1
			before := throttler.targetLookups
			allowance, err := throttler.Allowance(int64(period))
			if err != nil {
				t.Fatalf("Allowance(%d): %v", period, err)
			}
			lookups := throttler.targetLookups - before
			if lookups > 2 {
				t.Fatalf("target lookups=%d, want at most two adjacent reads", lookups)
			}
			t.Logf("input=Allowance(%d) output=%d lookups=%d reason=direct current/previous target reads", period, allowance, lookups)
		})
	}
}

func TestAcceptedOperationsDoNotTraverseTargets(t *testing.T) {
	for _, periods := range []int{100, 1440} {
		t.Run(fmt.Sprintf("periods=%d", periods), func(t *testing.T) {
			weights := make([]int64, periods)
			for i := range weights {
				weights[i] = 1
			}
			throttler := mustNew(t, 1_000_000_000_000, periods, 1, weights, 1234)
			period := periods - 1
			before := throttler.targetLookups
			charged, err := throttler.TryUpTo(1, int64(period))
			if err != nil || charged != 1 {
				t.Fatalf("TryUpTo(1,%d)=(%d,%v)", period, charged, err)
			}
			if lookups := throttler.targetLookups - before; lookups > 2 {
				t.Fatalf("accepted operation target lookups=%d, want at most two", lookups)
			}
		})
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	throttler := mustNew(t, 1000, 1, 100, []int64{1}, 0)
	const workers = 32
	var wg sync.WaitGroup
	accepted := make(chan int64, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			charged, err := throttler.TryUpTo(1, 0)
			if err == nil {
				accepted <- charged
			}
		}()
	}
	wg.Wait()
	close(accepted)

	var total int64
	for charged := range accepted {
		total += charged
	}
	if total != throttler.spent || total < 0 || total > 1000 {
		t.Fatalf("accepted total=%d spent=%d, want equal values within budget", total, throttler.spent)
	}
}
