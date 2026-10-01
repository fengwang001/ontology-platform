package ontology

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

func newExampleBilling(t *testing.T) *CumulativeBilling {
	t.Helper()
	billing, err := NewCumulativeBilling([]int64{1000, 5000}, []int64{10, 8, 5}, 3, 5000)
	if err != nil {
		t.Fatalf("NewCumulativeBilling() error = %v", err)
	}
	return billing
}

func assertFee(t *testing.T, billing *CumulativeBilling, acct, tid string, amount, want int64) {
	t.Helper()
	got, err := billing.Trade(acct, tid, amount)
	if err != nil {
		t.Fatalf("Trade(%q, %q, %d) error = %v", acct, tid, amount, err)
	}
	t.Logf("Trade input={account:%q tid:%q amount:%d} output={fee:%d} basis=Phi(cumulative)-Phi(previous cumulative), want=%d", acct, tid, amount, got, want)
	if got != want {
		t.Fatalf("Trade(%q, %q, %d) fee = %d, want %d", acct, tid, amount, got, want)
	}
}

func assertErrorIs(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestExampleCapAndCarry(t *testing.T) {
	billing := newExampleBilling(t)
	assertFee(t, billing, "a", "t1", 900, 0)
	assertFee(t, billing, "a", "t2", 1300, 1)
	assertFee(t, billing, "a", "t3", 3000, 2)

	oldFee, changes, err := billing.Cancel("t2")
	if err != nil {
		t.Fatalf("Cancel(t2) error = %v", err)
	}
	t.Logf("Cancel input={tid:t2} output={oldFee:%d changes:%v} basis=recompute following active trades from unchanged C0=0", oldFee, changes)
	if oldFee != 1 || len(changes) != 1 || changes[0].Tid != "t3" || changes[0].OldFee != 2 || changes[0].NewFee != 3 {
		t.Fatalf("Cancel(t2) = (%d, %+v), want old fee 1 and t3 2->3", oldFee, changes)
	}

	billing.NextPeriod()
	start, exists := billing.AccountStart("a")
	t.Logf("NextPeriod output={period:%d accountStart:%d exists:%v} basis=floor((C0+active amounts)*rho/10000)=floor(3900*5000/10000)", billing.CurrentPeriod(), start, exists)
	if !exists || start != 1950 || billing.CurrentPeriod() != 1 {
		t.Fatalf("after NextPeriod start=(%d,%v), period=%d; want 1950,true,1", start, exists, billing.CurrentPeriod())
	}

	assertFee(t, billing, "a", "t4", 2000, 2)
	assertFee(t, billing, "a", "t5", 1100, 0)
	oldFee, changes, err = billing.Cancel("t4")
	if err != nil {
		t.Fatalf("Cancel(t4) error = %v", err)
	}
	t.Logf("Cancel input={tid:t4} output={oldFee:%d changes:%v} basis=t5 recomputes from carried C0=1950", oldFee, changes)
	if oldFee != 2 || len(changes) != 1 || changes[0].Tid != "t5" || changes[0].OldFee != 0 || changes[0].NewFee != 1 {
		t.Fatalf("Cancel(t4) = (%d, %+v), want old fee 2 and t5 0->1", oldFee, changes)
	}
}

func TestTierBoundariesAndRounding(t *testing.T) {
	billing, err := NewCumulativeBilling([]int64{1000, 5000}, []int64{10, 8, 5}, 10_000, 0)
	if err != nil {
		t.Fatalf("NewCumulativeBilling() error = %v", err)
	}

	assertFee(t, billing, "a", "at-threshold", 1000, 1)
	assertFee(t, billing, "a", "second-zero", 1249, 0)
	assertFee(t, billing, "a", "carry-round", 1, 1)
	assertFee(t, billing, "c", "cross-two", 4500, 3)
	assertFee(t, billing, "d", "cross-into-last", 10_000, 6)

	assertFee(t, billing, "b", "before-first", 500, 0)
	assertFee(t, billing, "b", "cross-three", 6000, 4)

	if got := billing.uncappedFee(0); got != 0 {
		t.Fatalf("uncappedFee(0) = %d, want 0", got)
	}
}

func TestNextAmountFullyUsesLaterTier(t *testing.T) {
	billing, err := NewCumulativeBilling([]int64{1000, 5000}, []int64{10, 8, 5}, 10_000, 0)
	if err != nil {
		t.Fatalf("NewCumulativeBilling() error = %v", err)
	}
	assertFee(t, billing, "a", "at-boundary", 1000, 1)
	assertFee(t, billing, "a", "fully-second-tier", 4000, 3)
}

func TestConcurrentTradeSerializability(t *testing.T) {
	billing := newExampleBilling(t)
	var workers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for operation := 0; operation < 20; operation++ {
				tid := "concurrent-" + strconv.Itoa(worker) + "-" + strconv.Itoa(operation)
				if _, err := billing.Trade("shared", tid, 1_000); err != nil {
					t.Errorf("Trade(%s): %v", tid, err)
					return
				}
				_, _ = billing.AccountStart("shared")
				_ = billing.CurrentPeriod()
			}
		}(worker)
	}
	workers.Wait()

	account := billing.accounts["shared"]
	total := int64(0)
	feeSum := int64(0)
	for _, record := range account.trades {
		total += record.amount
		feeSum += record.fee
	}
	want := billing.cumulativeFee(total) - billing.cumulativeFee(0)
	t.Logf("concurrency output={trades:%d total:%d feeSum:%d} basis=some serial order gives Phi(total)-Phi(0)=%d", len(account.trades), total, feeSum, want)
	if total != 320_000 || feeSum != want {
		t.Fatalf("concurrent totals = (%d,%d), want (320000,%d)", total, feeSum, want)
	}
}

func TestCapZeroAndStartingCap(t *testing.T) {
	zeroCap, err := NewCumulativeBilling([]int64{1}, []int64{10_000, 10_000}, 0, 10_000)
	if err != nil {
		t.Fatalf("NewCumulativeBilling(zero cap) error = %v", err)
	}
	for _, amount := range []int64{1, 1_000_000_000, 1} {
		fee, tradeErr := zeroCap.Trade("a", uniqueTid(), amount)
		if tradeErr != nil || fee != 0 {
			t.Fatalf("zero cap Trade(%d) = (%d,%v), want 0,nil", amount, fee, tradeErr)
		}
	}

	startingCap := newExampleBilling(t)
	start := startingCap.cumulativeFee(6000)
	if start != 3 {
		t.Fatalf("setup cumulativeFee(6000) = %d, want 3", start)
	}
	startingCap.accounts["seed"] = &billingAccount{start: 6000}
	assertFee(t, startingCap, "seed", "after-cap-1", 1, 0)
	assertFee(t, startingCap, "seed", "after-cap-2", 1_000_000_000, 0)
}

func TestCapKeepsLaterZeroUntilCapMoves(t *testing.T) {
	billing := newExampleBilling(t)
	assertFee(t, billing, "a", "p1", 900, 0)
	assertFee(t, billing, "a", "p2", 1300, 1)
	assertFee(t, billing, "a", "p3", 3000, 2)
	assertFee(t, billing, "a", "p4", 1000, 0)

	oldFee, changes, err := billing.Cancel("p1")
	t.Logf("Cancel input={tid:p1} output={oldFee:%d changes:%v} basis=canceled fee was zero and cap still covers p4", oldFee, changes)
	if err != nil || oldFee != 0 || len(changes) != 0 {
		t.Fatalf("Cancel(p1) = (%d,%+v,%v), want 0,nil changes,nil", oldFee, changes, err)
	}
}

func TestCancelBeforeCapRestoresZeroFee(t *testing.T) {
	billing, err := NewCumulativeBilling([]int64{10}, []int64{10_000, 0}, 10, 10_000)
	if err != nil {
		t.Fatalf("NewCumulativeBilling() error = %v", err)
	}

	assertFee(t, billing, "a", "before", 5, 5)
	assertFee(t, billing, "a", "reaches-cap", 5, 5)
	assertFee(t, billing, "a", "after-cap", 100, 0)

	oldFee, changes, err := billing.Cancel("reaches-cap")
	if err != nil {
		t.Fatalf("Cancel(reaches-cap) error = %v", err)
	}
	t.Logf("Cancel input={tid:reaches-cap} output={oldFee:%d changes:%v} basis=removing five capped units lets after-cap collect five first-tier units", oldFee, changes)
	if oldFee != 5 || len(changes) != 1 {
		t.Fatalf("Cancel(reaches-cap) = (%d, %+v), want 5 and one change", oldFee, changes)
	}
	change := changes[0]
	if change.Tid != "after-cap" || change.OldFee != 0 || change.NewFee != 5 {
		t.Fatalf("change = %+v, want after-cap 0->5", change)
	}
	if got := billing.trades["after-cap"].fee; got != 5 {
		t.Fatalf("after-cap fee = %d, want 5", got)
	}
}

var testTidCounter int64

func uniqueTid() string {
	testTidCounter++
	return "tid-" + strconv.FormatInt(testTidCounter, 10)
}

func TestCancelPositionsAndInvariants(t *testing.T) {
	testCases := []struct {
		name        string
		cancel      int
		wantOld     int64
		wantFees    map[string]int64
		wantChanges map[string]FeeChange
		wantTotal   int64
	}{
		{name: "first", cancel: 0, wantOld: 0, wantFees: map[string]int64{"m1": 1, "l1": 2}, wantChanges: map[string]FeeChange{}, wantTotal: 3},
		{name: "middle", cancel: 1, wantOld: 1, wantFees: map[string]int64{"f1": 0, "l1": 3}, wantChanges: map[string]FeeChange{"l1": {Tid: "l1", OldFee: 2, NewFee: 3}}, wantTotal: 3},
		{name: "last", cancel: 2, wantOld: 2, wantFees: map[string]int64{"f1": 0, "m1": 1}, wantChanges: map[string]FeeChange{}, wantTotal: 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			billing := newExampleBilling(t)
			tids := []string{"f1", "m1", "l1"}
			amounts := []int64{900, 1300, 3000}
			for i, tid := range tids {
				assertFee(t, billing, "a", tid, amounts[i], []int64{0, 1, 2}[i])
			}

			oldFee, changes, err := billing.Cancel(tids[tc.cancel])
			if err != nil {
				t.Fatalf("Cancel(%s) error = %v", tids[tc.cancel], err)
			}
			if oldFee != tc.wantOld {
				t.Fatalf("oldFee = %d, want %d", oldFee, tc.wantOld)
			}
			if len(changes) != len(tc.wantChanges) {
				t.Fatalf("changes = %+v, want %+v", changes, tc.wantChanges)
			}
			for _, change := range changes {
				wantChange, ok := tc.wantChanges[change.Tid]
				if !ok || change != wantChange {
					t.Fatalf("changes = %+v, want %+v", changes, tc.wantChanges)
				}
			}

			for tid, wantFee := range tc.wantFees {
				record := billing.trades[tid]
				if record.fee != wantFee {
					t.Fatalf("%s fee after cancel = %d, want %d; changes=%v", tid, record.fee, wantFee, changes)
				}
			}

			cumulative := int64(0)
			feeSum := int64(0)
			for _, tid := range tids {
				record := billing.trades[tid]
				if record.active {
					cumulative += record.amount
					feeSum += record.fee
				}
			}
			wantSum := billing.cumulativeFee(cumulative) - billing.cumulativeFee(0)
			if feeSum != wantSum || feeSum != tc.wantTotal {
				t.Fatalf("fee sum = %d, invariant want %d, test want %d", feeSum, wantSum, tc.wantTotal)
			}
		})
	}
}

func TestValidationAndRejectedOperationsDoNotMutate(t *testing.T) {
	t.Run("constructor", func(t *testing.T) {
		invalid := [][]int64{{}, {0}, {5000, 1000}, {100000000000001}}
		for _, thresholds := range invalid {
			if _, err := NewCumulativeBilling(thresholds, make([]int64, len(thresholds)+1), 0, 0); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("thresholds %v: error = %v, want invalid", thresholds, err)
			}
		}
		for _, rates := range [][]int64{{10}, {-1, 8}, {10, 10001}} {
			if _, err := NewCumulativeBilling([]int64{1000}, rates, 0, 0); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("rates %v: error = %v, want invalid", rates, err)
			}
		}
		for _, capFee := range []int64{-1, 100_000_000_000_001} {
			if _, err := NewCumulativeBilling([]int64{1}, []int64{0, 0}, capFee, 0); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("cap %d: error = %v, want invalid", capFee, err)
			}
		}
		for _, carryRate := range []int64{-1, 10_001} {
			if _, err := NewCumulativeBilling([]int64{1}, []int64{0, 0}, 0, carryRate); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("rho %d: error = %v, want invalid", carryRate, err)
			}
		}
	})

	t.Run("trade errors", func(t *testing.T) {
		billing := newExampleBilling(t)
		if _, err := billing.Trade("", "x", 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("empty account: %v", err)
		}
		if _, err := billing.Trade("a", "", 1); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("empty tid: %v", err)
		}
		if _, err := billing.Trade("a", "x", 0); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("zero amount: %v", err)
		}
		if _, err := billing.Trade("a", "x", 1_000_000_001); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("amount over range: %v", err)
		}
		if _, err := billing.Trade("a", "dup", 1); err != nil {
			t.Fatalf("setup trade: %v", err)
		}
		if _, err := billing.Trade("a", "dup", 1); !errors.Is(err, ErrDuplicateTrade) {
			t.Fatalf("duplicate tid: %v", err)
		}
		billing.accounts["limit"] = &billingAccount{start: maxBillingAmount - 1}
		if _, err := billing.Trade("limit", "limit-ok", 1); err != nil {
			t.Fatalf("setup limit trade: %v", err)
		}
		if _, err := billing.Trade("limit", "limit-rejected", 2); !errors.Is(err, ErrCumulativeLimit) {
			t.Fatalf("cumulative limit: %v", err)
		}
		if _, exists := billing.trades["limit-rejected"]; exists {
			t.Fatal("rejected cumulative-limit trade was stored")
		}
	})

	t.Run("cancel errors and closed period", func(t *testing.T) {
		billing := newExampleBilling(t)
		if _, _, err := billing.Cancel(""); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("empty cancel tid: %v", err)
		}
		if _, _, err := billing.Cancel("missing"); !errors.Is(err, ErrTradeNotFound) {
			t.Fatalf("missing: %v", err)
		}
		assertFee(t, billing, "a", "gone", 1, 0)
		if _, _, err := billing.Cancel("gone"); err != nil {
			t.Fatalf("cancel setup: %v", err)
		}
		if _, _, err := billing.Cancel("gone"); !errors.Is(err, ErrTradeCanceled) {
			t.Fatalf("canceled: %v", err)
		}
		assertFee(t, billing, "b", "old", 1, 0)
		billing.NextPeriod()
		if _, _, err := billing.Cancel("old"); !errors.Is(err, ErrPeriodClosed) {
			t.Fatalf("closed period: %v", err)
		}
		if _, err := billing.Trade("b", "old", 1); !errors.Is(err, ErrDuplicateTrade) {
			t.Fatalf("reused canceled/closed tid: %v", err)
		}
	})
}

func TestCarryRoundingAndCompounding(t *testing.T) {
	billing, err := NewCumulativeBilling([]int64{1000}, []int64{0, 0}, 0, 3333)
	if err != nil {
		t.Fatalf("NewCumulativeBilling() error = %v", err)
	}
	billing.accounts["seed"] = &billingAccount{start: 10}
	billing.accounts["empty"] = &billingAccount{start: 10}

	billing.NextPeriod()
	assertStart(t, billing, "seed", 3)
	assertStart(t, billing, "empty", 3)
	billing.NextPeriod()
	assertStart(t, billing, "seed", 0)
	assertStart(t, billing, "empty", 0)

	zero, err := NewCumulativeBilling([]int64{1}, []int64{0, 0}, 0, 0)
	if err != nil {
		t.Fatalf("rho=0 constructor: %v", err)
	}
	zero.accounts["a"] = &billingAccount{start: 100}
	assertFee(t, zero, "a", "z1", 99, 0)
	zero.NextPeriod()
	assertStart(t, zero, "a", 0)

	full, err := NewCumulativeBilling([]int64{1}, []int64{0, 0}, 0, 10_000)
	if err != nil {
		t.Fatalf("rho=10000 constructor: %v", err)
	}
	full.accounts["a"] = &billingAccount{start: 7}
	assertFee(t, full, "a", "f1", 11, 0)
	full.NextPeriod()
	assertStart(t, full, "a", 18)
}

func assertStart(t *testing.T, billing *CumulativeBilling, acct string, want int64) {
	t.Helper()
	got, exists := billing.AccountStart(acct)
	t.Logf("AccountStart input={account:%q} output={start:%d exists:%v} basis=compounded floor(start*rho/10000), want=%d", acct, got, exists, want)
	if !exists || got != want {
		t.Fatalf("AccountStart(%q) = (%d,%v), want (%d,true)", acct, got, exists, want)
	}
}
