package card_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/card"
)

// Rejected operations must leave no trace: no state change, no clock
// change, no history entries.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	l := newLedger(t, card.Params{GraceDays: 2, MinPayRatio: 1000})
	mustCharge(t, l, card.Purchase, 1_000, 6)

	// Clock rollback: day 4 < last accepted day 6.
	_, err := l.Repay("a", 100, 4)
	wantErr(t, err, card.ErrClockRollback, "repay day=4 after day=6")

	// Billing at day 7 (due 9), then a too-early billing at day 8.
	mustBill(t, l, 7)
	_, err = l.Bill("a", 8)
	wantErr(t, err, card.ErrBillingTooEarly, "bill day=8 <= due day=9")

	// The rejected billing at day 8 must not have advanced the clock:
	// an operation at day 7 is still acceptable.
	mustCharge(t, l, card.Purchase, 1, 7)
	t.Logf("charge at day 7 accepted after rejected day-8 billing -> clock untouched (basis: rejected ops keep the clock)")

	// State so far: one bill, zero repayments, balance 1001.
	bills, _ := l.Bills("a")
	reps, _ := l.Repayments("a")
	if len(bills) != 1 || len(reps) != 0 {
		t.Fatalf("history: %d bills, %d repayments; want 1/0", len(bills), len(reps))
	}
	if s := snapshot(t, l); s.Balances[card.Purchase] != 1_001 {
		t.Fatalf("purchase balance=%d, want 1001", s.Balances[card.Purchase])
	}

	// A billing later than the due day succeeds.
	b2 := mustBill(t, l, 10)
	if b2.Seq != 1 {
		t.Fatalf("bill seq=%d, want 1", b2.Seq)
	}
}

// Error priority: invalid param > clock rollback > account not found >
// billing too early. Only the first applicable error is reported.
func TestErrorPriority(t *testing.T) {
	l := newLedger(t, card.Params{GraceDays: 2})
	mustCharge(t, l, card.Purchase, 1_000, 6)
	mustBill(t, l, 7) // due 9

	zeroAmount := func() error { _, err := l.Repay("ghost", 0, 3); return err }
	wantErr(t, zeroAmount(), card.ErrInvalidParam,
		"zero amount + rollback + unknown account -> invalid param")

	rollback := func() error { _, err := l.Repay("ghost", 100, 3); return err }
	wantErr(t, rollback(), card.ErrClockRollback,
		"rollback + unknown account -> clock rollback")

	notFound := func() error { _, err := l.Repay("ghost", 100, 8); return err }
	wantErr(t, notFound(), card.ErrAccountNotFound,
		"unknown account only -> account not found")

	tooEarly := func() error { _, err := l.Bill("a", 8); return err }
	wantErr(t, tooEarly(), card.ErrBillingTooEarly,
		"valid billing attempt before due -> billing too early")

	// Invalid category and negative now are invalid params.
	wantErr(t, l.Charge("a", card.Category(9), 1, 8), card.ErrInvalidParam, "bad category")
	wantErr(t, l.Charge("a", card.Cash, 1, -1), card.ErrInvalidParam, "negative day")
	wantErr(t, l.Charge("a", card.Cash, 0, 8), card.ErrInvalidParam, "zero charge")

	// Duplicate creation ranks after the clock check.
	wantErr(t, l.CreateAccount("a", card.Params{}, 8), card.ErrAccountExists, "duplicate account")
	wantErr(t, l.CreateAccount("b", card.Params{MinPayRatio: 20_001}, 8), card.ErrInvalidParam, "bad params")
	wantErr(t, l.CreateAccount("b", card.Params{}, 3), card.ErrClockRollback, "create with old day")
}

// Billing on the previous due day itself is too early; the day after is
// fine. GraceDays = 0 makes the due day equal to the billing day.
func TestBillingTooEarlyBoundary(t *testing.T) {
	l := newLedger(t, card.Params{GraceDays: 0})
	mustCharge(t, l, card.Cash, 100, 0)
	b1 := mustBill(t, l, 5)
	if b1.DueDay != 5 {
		t.Fatalf("due=%d, want 5", b1.DueDay)
	}
	if _, err := l.Bill("a", 5); err != card.ErrBillingTooEarly {
		t.Fatalf("bill same day: err=%v, want ErrBillingTooEarly", err)
	}
	mustBill(t, l, 6)
	t.Logf("due day 5: billing at 5 rejected, at 6 accepted (basis: billing day must be later than previous due)")
}

// Replaying the same operation sequence must reproduce identical bills,
// allocations and balances.
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]card.Bill, []card.Repayment, card.Snapshot) {
		l := card.NewLedger()
		p := card.Params{Rates: [3]int64{2400, 1200, 3650}, GraceDays: 3, MinPayRatio: 500, MinPayFloor: 100, LateFeeCap: 50}
		if err := l.CreateAccount("a", p, 0); err != nil {
			t.Fatal(err)
		}
		ops := []func() error{
			func() error { return l.Charge("a", card.Cash, 5_000, 1) },
			func() error { return l.Charge("a", card.Purchase, 9_000, 1) },
			func() error { return l.Charge("a", card.Installment, 3_000, 2) },
			func() error { _, err := l.Bill("a", 4); return err },
			func() error { _, err := l.Repay("a", 2_000, 5); return err },
			func() error { _, err := l.Repay("a", 500, 7); return err },
			func() error { _, err := l.Bill("a", 9); return err },
			func() error { _, err := l.Repay("a", 20_000, 10); return err },
			func() error { return l.Charge("a", card.Purchase, 700, 11) },
			func() error { _, err := l.Bill("a", 13); return err },
		}
		for _, op := range ops {
			if err := op(); err != nil {
				t.Fatal(err)
			}
		}
		bills, _ := l.Bills("a")
		reps, _ := l.Repayments("a")
		snap, _ := l.Snapshot("a")
		return bills, reps, snap
	}
	bills1, reps1, snap1 := run()
	bills2, reps2, snap2 := run()
	if len(bills1) != len(bills2) || len(reps1) != len(reps2) {
		t.Fatalf("history length mismatch")
	}
	for i := range bills1 {
		if bills1[i] != bills2[i] {
			t.Fatalf("bill %d differs: %+v vs %+v", i, bills1[i], bills2[i])
		}
	}
	for i := range reps1 {
		if reps1[i] != reps2[i] {
			t.Fatalf("repayment %d differs: %+v vs %+v", i, reps1[i], reps2[i])
		}
	}
	if snap1 != snap2 {
		t.Fatalf("snapshots differ: %+v vs %+v", snap1, snap2)
	}
	t.Logf("two identical replays produced identical bills, allocations and balances")
}

// Concurrent operations must be race-free, keep balances non-negative and
// behave as some serial order (guaranteed by the ledger mutex).
func TestConcurrentOps(t *testing.T) {
	l := card.NewLedger()
	p := card.Params{Rates: [3]int64{3000, 2000, 1000}, GraceDays: 1, MinPayRatio: 1000, MinPayFloor: 10, LateFeeCap: 100}
	if err := l.CreateAccount("a", p, 0); err != nil {
		t.Fatal(err)
	}
	if err := l.CreateAccount("b", p, 0); err != nil {
		t.Fatal(err)
	}

	var day atomic.Int64
	var wg sync.WaitGroup
	ids := []string{"a", "b"}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				now := day.Add(1)
				id := ids[(w+i)%2]
				switch (w + i) % 4 {
				case 0:
					_ = l.Charge(id, card.Category(i%3), int64(1+i%97), now)
				case 1:
					_, _ = l.Repay(id, int64(1+i%53), now)
				case 2:
					_, _ = l.Bill(id, now) // may be too early; fine
				case 3:
					_, _ = l.Snapshot(id)
				}
			}
		}(w)
	}
	wg.Wait()

	for _, id := range ids {
		s, err := l.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		for c, b := range s.Balances {
			if b < 0 {
				t.Fatalf("account %s: negative balance %d in category %d", id, b, c)
			}
		}
		if s.Overpayment < 0 {
			t.Fatalf("account %s: negative overpayment %d", id, s.Overpayment)
		}
	}
	t.Logf("8 goroutines x 500 ops completed; balances non-negative")
}

// Billing cost must depend only on the current period's transactions, not
// on history length. AccrualSteps counts every lazy accrual update; it is
// bounded by the number of accepted operations and grows strictly
// linearly in the operation count, independent of the number of past
// bills or repayments.
func TestBillingCostIndependentOfHistory(t *testing.T) {
	runPeriods := func(periods int) (steps, ops int64) {
		l := card.NewLedger()
		if err := l.CreateAccount("a", card.Params{Rates: [3]int64{3000, 2000, 1000}, GraceDays: 0, MinPayRatio: 1000}, 0); err != nil {
			t.Fatal(err)
		}
		day := int64(0)
		for p := 0; p < periods; p++ {
			day++
			if err := l.Charge("a", card.Purchase, 1_000, day); err != nil {
				t.Fatal(err)
			}
			day++
			if _, err := l.Repay("a", 300, day); err != nil {
				t.Fatal(err)
			}
			day++
			if _, err := l.Bill("a", day); err != nil {
				t.Fatal(err)
			}
			ops += 3
		}
		steps, err := l.AccrualSteps("a")
		if err != nil {
			t.Fatal(err)
		}
		return steps, ops
	}

	small, opsSmall := runPeriods(5)
	large, opsLarge := runPeriods(50)
	if small > opsSmall || large > opsLarge {
		t.Fatalf("accrual steps %d/%d exceed accepted ops %d/%d", small, large, opsSmall, opsLarge)
	}
	if large != 10*small {
		t.Fatalf("accrual steps %d (50 periods) != 10x %d (5 periods): cost depends on history", large, small)
	}
	t.Logf("accrual steps: 5 periods -> %d, 50 periods -> %d (exactly 10x, history-independent)", small, large)
}
