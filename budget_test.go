package retrybudget

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustBudget(t *testing.T, wd, wc, d, c, r, mx, cm int64) *RetryBudget {
	t.Helper()
	b, err := NewRetryBudget(wd, wc, d, c, r, mx, cm)
	if err != nil {
		t.Fatalf("NewRetryBudget() error = %v", err)
	}
	return b
}

func balanceOf(t *testing.T, b *RetryBudget, now int64) int64 {
	t.Helper()
	got, err := b.Balance(now)
	if err != nil {
		t.Fatalf("Balance(%d) error = %v", now, err)
	}
	return got
}

func allow(t *testing.T, b *RetryBudget, now int64, want bool) {
	t.Helper()
	got, err := b.TryRetry(now)
	if err != nil {
		t.Fatalf("TryRetry(%d) error = %v", now, err)
	}
	if got != want {
		t.Fatalf("TryRetry(%d) = %v, want %v", now, got, want)
	}
}

func TestSpecExample(t *testing.T) {
	b := mustBudget(t, 10, 20, 1, 2, 3, 3, 6)

	allow(t, b, 0, true)
	for now := int64(1); now <= 3; now++ {
		if err := b.Request(now); err != nil {
			t.Fatalf("Request(%d) error = %v", now, err)
		}
	}
	allow(t, b, 3, true)
	allow(t, b, 4, false)

	for now := int64(5); now <= 10; now++ {
		if err := b.Request(now); err != nil {
			t.Fatalf("Request(%d) error = %v", now, err)
		}
	}
	earlyChecks := []struct {
		now     int64
		balance int64
	}{
		{10, 3},
		{11, 3},
		{13, 3},
	}
	for _, check := range earlyChecks {
		if got := balanceOf(t, b, check.now); got != check.balance {
			t.Fatalf("Balance(%d) = %d, want %d", check.now, got, check.balance)
		}
	}
	if got := balanceOf(t, b, 15); got != 2 {
		t.Fatalf("Balance(15) = %d, want 2", got)
	}
	allow(t, b, 15, false)
	if got := balanceOf(t, b, 20); got != -1 {
		t.Fatalf("Balance(20) = %d, want -1", got)
	}
	allow(t, b, 20, false)
	allow(t, b, 23, true)
}

func TestInvalidConfig(t *testing.T) {
	valid := []int64{10, 20, 1, 2, 3, 3, 6}
	limits := [][2]int64{{1, 1_000_000_000}, {1, 1_000_000_000}, {1, 1_000_000}, {1, 1_000_000}, {0, 1_000_000_000}, {1, 10}, {1, 1_000_000}}
	for i, limit := range limits {
		for _, value := range []int64{limit[0] - 1, limit[1] + 1} {
			params := append([]int64(nil), valid...)
			params[i] = value
			if _, err := NewRetryBudget(params[0], params[1], params[2], params[3], params[4], params[5], params[6]); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("params[%d]=%d, error = %v, want ErrInvalidConfig", i, value, err)
			}
		}
	}
}

func TestInvalidTimeBeforeClockBacktrack(t *testing.T) {
	for _, op := range []string{"request", "retry", "balance"} {
		b := mustBudget(t, 10, 10, 1, 1, 1, 1, 1)
		if err := b.Request(5); err != nil {
			t.Fatal(err)
		}

		var err error
		switch op {
		case "request":
			err = b.Request(-1)
		case "retry":
			_, err = b.TryRetry(-1)
		case "balance":
			_, err = b.Balance(-1)
		}
		if !errors.Is(err, ErrInvalidTime) {
			t.Fatalf("%s negative time error = %v", op, err)
		}

		switch op {
		case "request":
			err = b.Request(4)
		case "retry":
			_, err = b.TryRetry(4)
		case "balance":
			_, err = b.Balance(4)
		}
		if !errors.Is(err, ErrClockBacktrack) {
			t.Fatalf("%s backtrack error = %v", op, err)
		}
	}
}

func TestEventBoundariesAndUnequalWindows(t *testing.T) {
	b := mustBudget(t, 10, 20, 4, 100, 100, 1, 10)
	if err := b.Request(0); err != nil {
		t.Fatal(err)
	}
	allow(t, b, 0, true)
	if got := balanceOf(t, b, 9); got != 4 {
		t.Fatalf("Balance(9) = %d, want 4", got)
	}
	if got := balanceOf(t, b, 10); got != 0 {
		t.Fatalf("Balance(10) = %d, want 0: deposit must expire exactly at t+Wd while consumption remains", got)
	}
	if got := balanceOf(t, b, 19); got != 0 {
		t.Fatalf("Balance(19) = %d, want 0", got)
	}
	if got := balanceOf(t, b, 20); got != 100 {
		t.Fatalf("Balance(20) = %d, want 100: consumption must expire exactly at t+Wc", got)
	}
}

func TestBalanceEqualCostAndOneShort(t *testing.T) {
	b := mustBudget(t, 10, 10, 2, 5, 3, 1, 10)
	if got := balanceOf(t, b, 0); got != 3 {
		t.Fatalf("Balance(0) = %d, want 3", got)
	}
	allow(t, b, 0, false)

	if err := b.Request(1); err != nil {
		t.Fatal(err)
	}
	if got := balanceOf(t, b, 1); got != 5 {
		t.Fatalf("Balance(1) = %d, want 5", got)
	}
	allow(t, b, 1, true)
}

func TestIncreasingCostAndMultiplierCap(t *testing.T) {
	b := mustBudget(t, 100, 100, 1, 10, 1_000, 3, 100)
	wantCosts := []int64{10, 20, 30, 30, 30}
	for i, wantCost := range wantCosts {
		now := int64(i)
		allow(t, b, now, true)
		latest := b.consumptions[len(b.consumptions)-1]
		if latest.cost != wantCost {
			t.Fatalf("retry %d cost = %d, want %d", i, latest.cost, wantCost)
		}
	}
}

func TestFrozenCostDoesNotRepriceAfterKFalls(t *testing.T) {
	b := mustBudget(t, 100, 25, 1, 10, 1_000, 3, 100)
	allow(t, b, 0, true)
	allow(t, b, 10, true)
	allow(t, b, 20, true)
	if got := b.frozenCost; got != 60 {
		t.Fatalf("frozen cost = %d, want 60", got)
	}
	if got := balanceOf(t, b, 34); got != 950 {
		t.Fatalf("Balance(34) = %d, want 950", got)
	}
	if got := len(b.consumptions) - b.consumeStart; got != 2 || b.frozenCost != 50 {
		t.Fatalf("before final expiry got events=%d frozen=%d, want 2 and 50", got, b.frozenCost)
	}
	allow(t, b, 35, true)
	latest := b.consumptions[len(b.consumptions)-1]
	if latest.cost != 20 {
		t.Fatalf("new cost after k fell = %d, want 20", latest.cost)
	}
	if b.frozenCost != 50 {
		t.Fatalf("frozen cost = %d, want 50; old costs must remain frozen", b.frozenCost)
	}
}

func TestDepositCapAndRecoveryAfterExpiry(t *testing.T) {
	b := mustBudget(t, 10, 100, 2, 100, 5, 2, 3)
	for now := int64(0); now < 5; now++ {
		if err := b.Request(now); err != nil {
			t.Fatal(err)
		}
	}
	if got := balanceOf(t, b, 5); got != 11 {
		t.Fatalf("Balance(5) = %d, want 11", got)
	}
	if got := balanceOf(t, b, 9); got != 11 {
		t.Fatalf("Balance(9) = %d, want 11", got)
	}
	if got := balanceOf(t, b, 10); got != 11 {
		t.Fatalf("Balance(10) = %d, want 11: only one event leaves but capped count stays at 3", got)
	}
	if got := balanceOf(t, b, 12); got != 9 {
		t.Fatalf("Balance(12) = %d, want 9", got)
	}
}

func TestNoReserveWithoutRequestRejects(t *testing.T) {
	b := mustBudget(t, 10, 10, 1, 1, 0, 10, 10)
	allow(t, b, 0, false)
	if len(b.consumptions) != 0 {
		t.Fatalf("rejected retry recorded %d events", len(b.consumptions))
	}
}

func TestRejectedRetryDoesNotChangeState(t *testing.T) {
	b := mustBudget(t, 10, 10, 1, 5, 3, 3, 10)
	beforeDeposits := len(b.deposits)
	beforeConsumptions := len(b.consumptions)
	beforeFrozen := b.frozenCost
	allow(t, b, 0, false)
	if len(b.deposits) != beforeDeposits || len(b.consumptions) != beforeConsumptions || b.frozenCost != beforeFrozen {
		t.Fatalf("rejected retry changed ledger state")
	}
	allow(t, b, 1, false)
	if len(b.consumptions) != 0 {
		t.Fatalf("rejected retries were counted in k: %d events", len(b.consumptions))
	}
}

func TestRequestThenRetryAtSameTime(t *testing.T) {
	b := mustBudget(t, 10, 10, 5, 5, 0, 1, 10)
	allow(t, b, 0, false)
	if err := b.Request(0); err != nil {
		t.Fatal(err)
	}
	allow(t, b, 0, true)
}

func TestRejectedInvalidOperationDoesNotAdvanceClock(t *testing.T) {
	b := mustBudget(t, 10, 10, 1, 1, 10, 1, 10)
	_, err := b.TryRetry(1_000_000_000_000_001)
	if !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("error = %v, want ErrInvalidTime", err)
	}
	if b.latestNow != 0 {
		t.Fatalf("latestNow = %d, want 0", b.latestNow)
	}
	if _, err := b.Balance(0); err != nil {
		t.Fatalf("valid operation after rejected invalid time: %v", err)
	}
}

func TestConcurrentRequestsThenRetries(t *testing.T) {
	b := mustBudget(t, 100, 100, 1, 1, 1_000, 10, 100)
	const count = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2 * count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			<-start
			if err := b.Request(0); err != nil {
				t.Errorf("Request: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if allowed, err := b.TryRetry(0); err != nil || !allowed {
				t.Errorf("TryRetry allowed=%v err=%v", allowed, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(b.deposits)-b.depositStart != count {
		t.Fatalf("deposits=%d, want %d", len(b.deposits)-b.depositStart, count)
	}
	if len(b.consumptions)-b.consumeStart != count {
		t.Fatalf("consumptions=%d, want %d", len(b.consumptions)-b.consumeStart, count)
	}
	want := b.reserve + b.depositAmount*int64(count) - b.frozenCost
	if got := balanceOf(t, b, 0); got != want {
		t.Fatalf("Balance(0) = %d, want %d", got, want)
	}
}

func TestCountersExposedForAmortizationCheck(t *testing.T) {
	b := mustBudget(t, 1, 1, 1, 1, 1_000, 1, 100)
	for now := int64(0); now < 10; now++ {
		if err := b.Request(now); err != nil {
			t.Fatal(err)
		}
		allow(t, b, now, true)
	}
	if b.cleanedCount > int64(len(b.deposits)+len(b.consumptions))+b.cleanedCount {
		t.Fatal("cleaned events exceeded registered events")
	}
	t.Logf("cleaned=%d inspected=%d operations=30", b.cleanedCount, b.inspectedCount)
}

func TestAmortizedCleaningBounds(t *testing.T) {
	for _, operations := range []int64{1000, 100_000} {
		t.Run(fmt.Sprintf("%d", operations), func(t *testing.T) {
			b := mustBudget(t, 1, 2, 1, 1, 1_000_000_000, 10, 1_000_000)
			var registered int64
			for now := int64(0); now < operations; now++ {
				if err := b.Request(now); err != nil {
					t.Fatal(err)
				}
				registered++
				allowed, err := b.TryRetry(now)
				if err != nil || !allowed {
					t.Fatalf("TryRetry(%d) allowed=%t err=%v", now, allowed, err)
				}
				registered++
			}

			if b.cleanedCount > registered {
				t.Fatalf("cleaned=%d exceeds registered=%d", b.cleanedCount, registered)
			}
			constantBound := int64(10) * operations
			if b.inspectedCount > constantBound {
				t.Fatalf("inspected=%d exceeds constant bound=%d", b.inspectedCount, constantBound)
			}
			t.Logf("operations=%d registered=%d cleaned=%d inspected=%d ratio=%.3f",
				2*operations, registered, b.cleanedCount, b.inspectedCount,
				float64(b.inspectedCount)/float64(2*operations))
		})
	}
}

func TestLongActiveLedgerDoesNotMakeOperationsLinear(t *testing.T) {
	b := mustBudget(t, 1_000_000_000, 1_000_000_000, 1, 1, 1_000_000_000, 10, 1_000_000)
	for i := 0; i < 100_000; i++ {
		if err := b.Request(0); err != nil {
			t.Fatal(err)
		}
		allowed, err := b.TryRetry(0)
		if err != nil || !allowed {
			t.Fatalf("TryRetry allowed=%t err=%v", allowed, err)
		}
	}

	b.cleanedCount = 0
	b.inspectedCount = 0
	if _, err := b.Balance(0); err != nil {
		t.Fatal(err)
	}
	if b.cleanedCount != 0 {
		t.Fatalf("cleaned active events=%d, want 0", b.cleanedCount)
	}
	if b.inspectedCount != 2 {
		t.Fatalf("inspected=%d, want 2: pruning must stop at first active event in each queue", b.inspectedCount)
	}
}

func ExampleRetryBudget() {
	b, _ := NewRetryBudget(10, 20, 1, 2, 3, 3, 6)
	allowed, _ := b.TryRetry(0)
	balance, _ := b.Balance(0)
	fmt.Println(allowed, balance)
	// Output: true 1
}
