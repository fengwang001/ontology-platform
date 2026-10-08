package card_test

import (
	"errors"
	"testing"

	"ontology/card"
)

// --- helpers -------------------------------------------------------------

func newLedger(t *testing.T, p card.Params) *card.Ledger {
	t.Helper()
	l := card.NewLedger()
	if err := l.CreateAccount("a", p, 0); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	t.Logf("create account a day=0 params=%+v", p)
	return l
}

func mustCharge(t *testing.T, l *card.Ledger, cat card.Category, amount, day int64) {
	t.Helper()
	if err := l.Charge("a", cat, amount, day); err != nil {
		t.Fatalf("Charge(%s, %d, day=%d): %v", cat, amount, day, err)
	}
	t.Logf("day=%d charge %s %d -> ok", day, cat, amount)
}

func mustBill(t *testing.T, l *card.Ledger, day int64) card.Bill {
	t.Helper()
	b, err := l.Bill("a", day)
	if err != nil {
		t.Fatalf("Bill(day=%d): %v", day, err)
	}
	t.Logf("day=%d bill -> total=%d minDue=%d due=%d interest=%v lateFee=%d paidInWindow=%d",
		day, b.Total, b.MinDue, b.DueDay, b.Interest, b.LateFee, b.PaidInWindow)
	return b
}

func mustRepay(t *testing.T, l *card.Ledger, amount, day int64) card.Repayment {
	t.Helper()
	r, err := l.Repay("a", amount, day)
	if err != nil {
		t.Fatalf("Repay(%d, day=%d): %v", amount, day, err)
	}
	t.Logf("day=%d repay %d -> alloc=%v toMin=%d over=%d", day, amount, r.Alloc, r.ToMin, r.Over)
	return r
}

func snapshot(t *testing.T, l *card.Ledger) card.Snapshot {
	t.Helper()
	s, err := l.Snapshot("a")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return s
}

func wantErr(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got error %v, want %v", what, got, want)
	}
	t.Logf("%s -> rejected with %v (expected)", what, got)
}

// --- interest ------------------------------------------------------------

// Interest remainder must carry across periods and never be lost:
// cash rate 365bp on balance 100 accrues exactly 36500/day, i.e. half a
// currency unit per 50-day period; two periods sum to exactly 1.
func TestInterestRemainderCarriesAcrossPeriods(t *testing.T) {
	l := newLedger(t, card.Params{Rates: [3]int64{365, 0, 0}})
	mustCharge(t, l, card.Cash, 100, 0)

	b1 := mustBill(t, l, 50)
	// base = 100 * 365 * 50 = 1_825_000 < 3_650_000 -> interest 0,
	// remainder 1_825_000 carried.
	if b1.Interest != [3]int64{0, 0, 0} || b1.Total != 100 {
		t.Fatalf("bill1: interest=%v total=%d, want [0 0 0]/100", b1.Interest, b1.Total)
	}
	t.Logf("bill1: base 1825000 < unit 3650000 -> interest 0, remainder carried")

	b2 := mustBill(t, l, 100)
	// base 1_825_000 + carry 1_825_000 = 3_650_000 -> interest exactly 1.
	if b2.Interest[card.Cash] != 1 || b2.Total != 101 {
		t.Fatalf("bill2: interest=%v total=%d, want cash interest 1, total 101", b2.Interest, b2.Total)
	}
	t.Logf("bill2: base 1825000 + carried 1825000 = 3650000 -> interest 1, remainder 0")

	// Remainder keeps accumulating from the new balance (101).
	b3 := mustBill(t, l, 150)
	if b3.Interest[card.Cash] != 0 {
		t.Fatalf("bill3: cash interest=%d, want 0 (101*365*50=1843250 < unit)", b3.Interest[card.Cash])
	}
	b4 := mustBill(t, l, 250)
	// 1_843_250 + 1_843_250 = 3_686_500 -> interest 1, remainder 36_500.
	if b4.Interest[card.Cash] != 1 {
		t.Fatalf("bill4: cash interest=%d, want 1 (1843250+carry 1843250=3686500)", b4.Interest[card.Cash])
	}
	if s := snapshot(t, l); s.Balances[card.Cash] != 102 {
		t.Fatalf("cash balance=%d, want 102", s.Balances[card.Cash])
	}
	t.Logf("bill3+bill4: remainder 1843250 carried, then 3686500 -> interest 1, remainder 36500 kept")
}

// Purchase interest is waived only when the previous bill was fully repaid
// by its due day (inclusive). Repaying on the due day counts; repaying one
// day later does not, and additionally triggers the late fee.
func TestDueDayRepaymentVsNextDay(t *testing.T) {
	params := card.Params{
		Rates:       [3]int64{0, 0, 1800},
		GraceDays:   10,
		MinPayRatio: 1000,
		LateFeeCap:  500,
	}
	setup := func(t *testing.T) *card.Ledger {
		l := newLedger(t, params)
		mustCharge(t, l, card.Purchase, 100_000, 0)
		b1 := mustBill(t, l, 10)
		// First period: purchase interest waived (initial fully-paid).
		if b1.Total != 100_000 || b1.MinDue != 10_000 || b1.DueDay != 20 {
			t.Fatalf("bill1: total=%d minDue=%d due=%d, want 100000/10000/20",
				b1.Total, b1.MinDue, b1.DueDay)
		}
		mustCharge(t, l, card.Purchase, 50_000, 15)
		return l
	}

	t.Run("repay_on_due_day_counts", func(t *testing.T) {
		l := setup(t)
		mustRepay(t, l, 100_000, 20) // exactly on the due day
		b2 := mustBill(t, l, 25)
		// Fully repaid in window -> purchase interest waived, no late fee.
		if b2.Interest != [3]int64{0, 0, 0} || b2.LateFee != 0 {
			t.Fatalf("bill2: interest=%v lateFee=%d, want 0/0", b2.Interest, b2.LateFee)
		}
		if b2.Total != 50_000 || b2.MinDue != 5_000 {
			t.Fatalf("bill2: total=%d minDue=%d, want 50000/5000", b2.Total, b2.MinDue)
		}
		t.Logf("repay on due day 20 counts -> waived, no late fee (basis: window includes due day)")
	})

	t.Run("repay_one_day_late_does_not_count", func(t *testing.T) {
		l := setup(t)
		mustRepay(t, l, 100_000, 21) // one day after the due day
		b2 := mustBill(t, l, 25)
		// Purchase base: days 11-15 @100000, 16-21 @150000, 22-25 @50000
		// = 1_600_000 balance-days * 1800 = 2_880_000_000
		// interest = floor(2_880_000_000 / 3_650_000) = 789.
		if b2.Interest[card.Purchase] != 789 {
			t.Fatalf("bill2: purchase interest=%d, want 789", b2.Interest[card.Purchase])
		}
		// Late fee: min(500, 10_000 - 0) = 500, added to purchase.
		if b2.LateFee != 500 {
			t.Fatalf("bill2: lateFee=%d, want 500", b2.LateFee)
		}
		if b2.Total != 51_289 || b2.MinDue != 6_289 {
			t.Fatalf("bill2: total=%d minDue=%d, want 51289/6289", b2.Total, b2.MinDue)
		}
		t.Logf("repay on day 21 misses window -> interest 789 + late fee 500 (basis: window closed at day 20)")
	})
}

// Minimum payment boundary: repaying exactly MinDue avoids the late fee;
// one unit less triggers min(F, shortfall).
func TestMinPaymentExactVsOneShort(t *testing.T) {
	params := card.Params{
		GraceDays:   5,
		MinPayRatio: 1000,
		LateFeeCap:  500,
	}
	setup := func(t *testing.T) *card.Ledger {
		l := newLedger(t, params)
		mustCharge(t, l, card.Purchase, 100_000, 0)
		b1 := mustBill(t, l, 3)
		if b1.MinDue != 10_000 || b1.DueDay != 8 {
			t.Fatalf("bill1: minDue=%d due=%d, want 10000/8", b1.MinDue, b1.DueDay)
		}
		return l
	}

	t.Run("exact_minimum_no_late_fee", func(t *testing.T) {
		l := setup(t)
		mustRepay(t, l, 10_000, 8)
		b2 := mustBill(t, l, 10)
		if b2.LateFee != 0 {
			t.Fatalf("bill2: lateFee=%d, want 0 (paid exactly the minimum)", b2.LateFee)
		}
	})

	t.Run("one_unit_short_pays_late_fee", func(t *testing.T) {
		l := setup(t)
		mustRepay(t, l, 9_999, 8)
		b2 := mustBill(t, l, 10)
		// shortfall = 1 -> lateFee = min(500, 1) = 1, added to purchase.
		if b2.LateFee != 1 {
			t.Fatalf("bill2: lateFee=%d, want 1 = min(500, 10000-9999)", b2.LateFee)
		}
		// minDue = interest 0 + lateFee 1 + ceil(90001 * 0.1) = 9002.
		if b2.Total != 90_002 || b2.MinDue != 9_002 {
			t.Fatalf("bill2: total=%d minDue=%d, want 90002/9002", b2.Total, b2.MinDue)
		}
		t.Logf("shortfall 1 -> lateFee min(F=500, 1)=1 (basis: window sum 9999 < minDue 10000)")
	})
}

// Full repayment boundary: window sum exactly equal to the bill total
// counts as fully paid; one unit less does not.
func TestFullPaymentExactTotalBoundary(t *testing.T) {
	params := card.Params{
		Rates:       [3]int64{0, 0, 3650},
		GraceDays:   5,
		MinPayRatio: 1000,
	}
	setup := func(t *testing.T) *card.Ledger {
		l := newLedger(t, params)
		mustCharge(t, l, card.Purchase, 50_000, 0)
		b1 := mustBill(t, l, 2)
		if b1.Total != 50_000 || b1.MinDue != 5_000 || b1.DueDay != 7 {
			t.Fatalf("bill1: total=%d minDue=%d due=%d", b1.Total, b1.MinDue, b1.DueDay)
		}
		return l
	}

	t.Run("exact_total_fully_paid", func(t *testing.T) {
		l := setup(t)
		mustRepay(t, l, 50_000, 7) // exactly the bill total, on the due day
		mustCharge(t, l, card.Purchase, 20_000, 8)
		b2 := mustBill(t, l, 12)
		if b2.Interest[card.Purchase] != 0 {
			t.Fatalf("bill2: purchase interest=%d, want 0 (previous bill fully paid)", b2.Interest[card.Purchase])
		}
		if b2.Total != 20_000 {
			t.Fatalf("bill2: total=%d, want 20000", b2.Total)
		}
	})

	t.Run("one_unit_short_not_fully_paid", func(t *testing.T) {
		l := setup(t)
		mustRepay(t, l, 49_999, 7) // one less than the total
		mustCharge(t, l, card.Purchase, 20_000, 8)
		b2 := mustBill(t, l, 12)
		// base: days 3-7 @50000, day 8 @1, days 9-12 @20001
		// = 330_005 balance-days * 3650 = 1_204_518_250 -> interest 330.
		if b2.Interest[card.Purchase] != 330 {
			t.Fatalf("bill2: purchase interest=%d, want 330", b2.Interest[card.Purchase])
		}
		if b2.LateFee != 0 {
			t.Fatalf("bill2: lateFee=%d, want 0 (minimum was paid)", b2.LateFee)
		}
		if b2.Total != 20_331 || b2.MinDue != 2_331 {
			t.Fatalf("bill2: total=%d minDue=%d, want 20331/2331", b2.Total, b2.MinDue)
		}
		t.Logf("window sum 49999 < total 50000 -> not fully paid -> interest 330 (basis: exact-total boundary)")
	})
}

// The purchase interest waiver depends only on the previous bill, never on
// the current period's own repayments.
func TestGraceLooksOnlyAtPreviousBill(t *testing.T) {
	params := card.Params{
		Rates:       [3]int64{0, 0, 3650},
		GraceDays:   5,
		MinPayRatio: 1000,
	}

	t.Run("current_period_full_repay_does_not_waive", func(t *testing.T) {
		l := newLedger(t, params)
		mustCharge(t, l, card.Purchase, 10_000, 0)
		mustBill(t, l, 2) // total 10000, minDue 1000, due 7; never repaid
		mustCharge(t, l, card.Purchase, 5_000, 8)
		mustRepay(t, l, 15_000, 9) // repays everything, but too late for bill1
		b2 := mustBill(t, l, 12)
		// base: days 3-8 @10000, day 9 @15000 = 75_000 * 3650 -> interest 75.
		if b2.Interest[card.Purchase] != 75 || b2.Total != 75 {
			t.Fatalf("bill2: interest=%v total=%d, want purchase interest 75, total 75",
				b2.Interest, b2.Total)
		}
		t.Logf("bill1 unpaid by due -> interest 75 despite full repay on day 9 (basis: waiver only looks at previous bill)")
	})

	t.Run("previous_paid_waives_without_any_current_repay", func(t *testing.T) {
		l := newLedger(t, params)
		mustCharge(t, l, card.Purchase, 10_000, 0)
		mustBill(t, l, 2)
		mustRepay(t, l, 10_000, 7) // fully pays bill1 on the due day
		mustCharge(t, l, card.Purchase, 5_000, 8)
		// No repayment at all in period 2.
		b2 := mustBill(t, l, 12)
		if b2.Interest[card.Purchase] != 0 || b2.Total != 5_000 {
			t.Fatalf("bill2: interest=%v total=%d, want 0/5000", b2.Interest, b2.Total)
		}
		t.Logf("bill1 fully paid -> interest 0 with zero repayments in period 2 (basis: waiver only looks at previous bill)")
	})
}

// One repayment can span the minimum portion (fixed category order) and
// the excess portion (rate order); a later repayment overflows to
// overpayment.
func TestRepaymentAllocationSplit(t *testing.T) {
	l := newLedger(t, card.Params{
		Rates:       [3]int64{3600, 1800, 500},
		GraceDays:   10,
		MinPayRatio: 1000,
	})
	mustCharge(t, l, card.Cash, 1_000, 0)
	mustCharge(t, l, card.Installment, 2_000, 0)
	mustCharge(t, l, card.Purchase, 30_000, 0)

	b1 := mustBill(t, l, 10)
	// cash: 1000*3600*10 = 36_000_000 -> interest 9 (carry 3_150_000)
	// installment: 2000*1800*10 = 36_000_000 -> interest 9
	// purchase: waived (first period)
	if b1.Interest != [3]int64{9, 9, 0} {
		t.Fatalf("bill1: interest=%v, want [9 9 0]", b1.Interest)
	}
	// total 33018; minDue = 18 + ceil(33000*0.1) = 3318.
	if b1.Total != 33_018 || b1.MinDue != 3_318 {
		t.Fatalf("bill1: total=%d minDue=%d, want 33018/3318", b1.Total, b1.MinDue)
	}

	r := mustRepay(t, l, 5_000, 15)
	// Minimum portion 3318 in fixed order: cash 1009, installment 2009,
	// purchase 300. Excess 1682 by rate order: cash/installment empty,
	// so all to purchase -> purchase 300+1682 = 1982.
	if r.ToMin != 3_318 || r.Alloc != [3]int64{1_009, 2_009, 1_982} || r.Over != 0 {
		t.Fatalf("repay: toMin=%d alloc=%v over=%d, want 3318/[1009 2009 1982]/0",
			r.ToMin, r.Alloc, r.Over)
	}
	if s := snapshot(t, l); s.Balances != [3]int64{0, 0, 28_018} {
		t.Fatalf("balances=%v, want [0 0 28018]", s.Balances)
	}
	t.Logf("split: min part fixed order cash->installment->purchase, excess by rate desc (basis: allocation rules)")

	r = mustRepay(t, l, 40_000, 16)
	// Minimum already satisfied; everything by rate order, then overflow.
	if r.ToMin != 0 || r.Alloc != [3]int64{0, 0, 28_018} || r.Over != 11_982 {
		t.Fatalf("repay: toMin=%d alloc=%v over=%d, want 0/[0 0 28018]/11982",
			r.ToMin, r.Alloc, r.Over)
	}
	if s := snapshot(t, l); s.Overpayment != 11_982 {
		t.Fatalf("overpayment=%d, want 11982", s.Overpayment)
	}
}

// Overpayment offsets later charges before any balance forms.
func TestOverpaymentOffsetsCharges(t *testing.T) {
	l := newLedger(t, card.Params{GraceDays: 5, MinPayRatio: 1000})
	mustCharge(t, l, card.Purchase, 10_000, 0)
	r := mustRepay(t, l, 15_000, 1)
	if r.Alloc != [3]int64{0, 0, 10_000} || r.Over != 5_000 {
		t.Fatalf("repay: alloc=%v over=%d, want [0 0 10000]/5000", r.Alloc, r.Over)
	}

	mustCharge(t, l, card.Cash, 3_000, 2) // fully covered by overpayment
	if s := snapshot(t, l); s.Balances != [3]int64{0, 0, 0} || s.Overpayment != 2_000 {
		t.Fatalf("after offset charge: balances=%v over=%d, want [0 0 0]/2000", s.Balances, s.Overpayment)
	}

	mustCharge(t, l, card.Purchase, 5_000, 3) // 2000 from overpayment, 3000 balance
	if s := snapshot(t, l); s.Balances != [3]int64{0, 0, 3_000} || s.Overpayment != 0 {
		t.Fatalf("after partial offset: balances=%v over=%d, want [0 0 3000]/0", s.Balances, s.Overpayment)
	}
	t.Logf("overpayment 5000 -> offset 3000 + 2000, remainder forms balance (basis: charge offsets overpayment first)")
}
