package creditcard

import (
	"testing"
)

func mustCreate(t *testing.T, s *Service, id string, p Params, now int64) {
	t.Helper()
	if err := s.CreateAccount(id, p, now); err != nil {
		t.Fatalf("CreateAccount(%q) at %d: %v", id, now, err)
	}
}

func mustCharge(t *testing.T, s *Service, id string, c Category, amount, now int64) {
	t.Helper()
	if err := s.Charge(id, c, amount, now); err != nil {
		t.Fatalf("Charge(%q,%v,%d) at %d: %v", id, c, amount, now, err)
	}
}

func mustPay(t *testing.T, s *Service, id string, amount, now int64) PaymentRecord {
	t.Helper()
	rec, err := s.Pay(id, amount, now)
	if err != nil {
		t.Fatalf("Pay(%q,%d) at %d: %v", id, amount, now, err)
	}
	return rec
}

func mustBill(t *testing.T, s *Service, id string, now int64) Bill {
	t.Helper()
	b, err := s.Bill(id, now)
	if err != nil {
		t.Fatalf("Bill(%q) at %d: %v", id, now, err)
	}
	return b
}

func balancesOf(t *testing.T, s *Service, id string) [NumCategories]int64 {
	t.Helper()
	b, err := s.Balances(id)
	if err != nil {
		t.Fatalf("Balances(%q): %v", id, err)
	}
	return b
}

func overOf(t *testing.T, s *Service, id string) int64 {
	t.Helper()
	o, err := s.Overpayment(id)
	if err != nil {
		t.Fatalf("Overpayment(%q): %v", id, err)
	}
	return o
}

func billsOf(t *testing.T, s *Service, id string) []Bill {
	t.Helper()
	b, err := s.Bills(id)
	if err != nil {
		t.Fatalf("Bills(%q): %v", id, err)
	}
	return b
}

func paymentsOf(t *testing.T, s *Service, id string) []PaymentRecord {
	t.Helper()
	p, err := s.Payments(id)
	if err != nil {
		t.Fatalf("Payments(%q): %v", id, err)
	}
	return p
}

func zeroRates(grace, ratioBps, floor, cap int64) Params {
	return Params{GraceDays: grace, MinPayRatioBps: ratioBps, MinPayFloor: floor, LateFeeCap: cap}
}

// A payment on the due date counts toward the bill; one day later it does
// not, so the late fee applies.
func TestDueDatePaymentVsNextDay(t *testing.T) {
	s := NewService()
	p := zeroRates(5, 1000, 0, 50)
	mustCreate(t, s, "A", p, 0)
	mustCreate(t, s, "B", p, 0)
	mustCharge(t, s, "A", Purchase, 1000, 0)
	mustCharge(t, s, "B", Purchase, 1000, 0)
	b1A := mustBill(t, s, "A", 1) // total 1000, min 100, due 6
	mustBill(t, s, "B", 1)
	if b1A.Total != 1000 || b1A.MinPayment != 100 || b1A.DueDate != 6 {
		t.Fatalf("unexpected bill A: %+v", b1A)
	}

	mustPay(t, s, "A", 100, 6) // on the due date: counts
	mustPay(t, s, "B", 100, 7) // one day after: does not count

	b2A := mustBill(t, s, "A", 7)
	b2B := mustBill(t, s, "B", 8)
	if b2A.LateFee != 0 {
		t.Fatalf("A: due-date payment must avoid late fee, got %d", b2A.LateFee)
	}
	if b2B.LateFee != 50 {
		t.Fatalf("B: next-day payment must incur late fee 50, got %d", b2B.LateFee)
	}
	if got := billsOf(t, s, "B")[0].PaidByDueDate; got != 0 {
		t.Fatalf("B: PaidByDueDate = %d, want 0 (payment was after due date)", got)
	}
	if got := billsOf(t, s, "B")[0].PaidTotal; got != 100 {
		t.Fatalf("B: PaidTotal = %d, want 100", got)
	}
	if b2A.Total != 900 || b2B.Total != 950 {
		t.Fatalf("totals: A=%d (want 900) B=%d (want 950)", b2A.Total, b2B.Total)
	}
}

// Paying exactly the minimum avoids the late fee; one unit less triggers
// a late fee of exactly the one-unit shortfall (below the cap).
func TestExactMinPaymentVsOneLess(t *testing.T) {
	s := NewService()
	p := zeroRates(5, 1000, 0, 50)
	mustCreate(t, s, "A", p, 0)
	mustCreate(t, s, "B", p, 0)
	mustCharge(t, s, "A", Purchase, 1000, 0)
	mustCharge(t, s, "B", Purchase, 1000, 0)
	mustBill(t, s, "A", 1) // min 100, due 6
	mustBill(t, s, "B", 1)

	mustPay(t, s, "A", 100, 5) // exactly the minimum
	mustPay(t, s, "B", 99, 5)  // one less

	if b := mustBill(t, s, "A", 7); b.LateFee != 0 {
		t.Fatalf("A: exact minimum must avoid late fee, got %d", b.LateFee)
	}
	b2B := mustBill(t, s, "B", 7)
	if b2B.LateFee != 1 {
		t.Fatalf("B: late fee = %d, want 1 (shortfall below cap)", b2B.LateFee)
	}
	if b2B.Total != 901+1 {
		t.Fatalf("B: total = %d, want 902", b2B.Total)
	}
}

// Paying exactly the statement total by the due date keeps the
// interest-free grace; one unit less loses it.
func TestFullPaymentExactlyTotal(t *testing.T) {
	s := NewService()
	p := zeroRates(5, 10000, 0, 0)
	p.RateBps = [NumCategories]int64{0, 0, 36500} // purchase: 1 unit per 100 per day
	mustCreate(t, s, "A", p, 0)
	mustCreate(t, s, "B", p, 0)
	mustCharge(t, s, "A", Purchase, 10000, 0)
	mustCharge(t, s, "B", Purchase, 10000, 0)
	mustBill(t, s, "A", 1) // total 10000, due 6
	mustBill(t, s, "B", 1)

	mustPay(t, s, "A", 10000, 6) // exactly the total, on the due date
	mustPay(t, s, "B", 9999, 6)  // one less

	b2A := mustBill(t, s, "A", 7)
	if b2A.Interest[Purchase] != 0 {
		t.Fatalf("A: fully paid bill must keep grace, got interest %d", b2A.Interest[Purchase])
	}
	b2B := mustBill(t, s, "B", 7)
	// B: day-6 payment does not change day 6's base (start-of-day rule):
	// balance 10000 on days 2..6 (5 days), 1 on day 7.
	// base = (5*10000 + 1) * 36500 = 1_825_036_500 -> interest 500.
	if b2B.Interest[Purchase] != 500 {
		t.Fatalf("B: interest = %d, want 500", b2B.Interest[Purchase])
	}
	if got := balancesOf(t, s, "B"); got[Purchase] != 501 {
		t.Fatalf("B: purchase balance = %d, want 501", got[Purchase])
	}
}

// Grace depends only on whether the previous bill was fully paid by its
// due date; payments inside the current period are irrelevant.
func TestGraceLooksOnlyAtPreviousBill(t *testing.T) {
	s := NewService()
	p := zeroRates(10, 10000, 0, 100)
	p.RateBps = [NumCategories]int64{0, 0, 36500}
	mustCreate(t, s, "x", p, 0)
	mustCharge(t, s, "x", Purchase, 10000, 0)

	b1 := mustBill(t, s, "x", 10) // total 10000, min 10000, due 20
	mustCharge(t, s, "x", Purchase, 5000, 12)
	mustPay(t, s, "x", 10000, 15) // fully pays bill 1 within its window

	b2 := mustBill(t, s, "x", 25) // prev fully paid -> purchase interest waived
	if b2.Interest[Purchase] != 0 {
		t.Fatalf("bill2: grace must waive purchase interest, got %d", b2.Interest[Purchase])
	}
	if b2.Total != 5000 || b2.MinPayment != 5000 || b2.DueDate != 35 {
		t.Fatalf("unexpected bill2: %+v", b2)
	}

	mustPay(t, s, "x", 1000, 30) // inside bill2 window (<=35)
	mustPay(t, s, "x", 2000, 38) // after bill2 due date: does not help bill2

	b3 := mustBill(t, s, "x", 40)
	// bill2 window sum = 1000 < 5000 -> interest charged for period 3:
	// start-of-day balances: 5000 on 26..30 (5d), 4000 on 31..38 (8d),
	// 2000 on 39..40 (2d).
	// base = (25000+32000+4000)*36500 = 2_226_500_000 -> interest 610.
	if b3.Interest[Purchase] != 610 {
		t.Fatalf("bill3: interest = %d, want 610", b3.Interest[Purchase])
	}
	// Late fee: bill2 paid in window 1000 < min 5000 -> min(100, 4000).
	if b3.LateFee != 100 {
		t.Fatalf("bill3: late fee = %d, want 100", b3.LateFee)
	}
	if b3.Total != 2000+610+100 {
		t.Fatalf("bill3: total = %d, want 2710", b3.Total)
	}
	if got := balancesOf(t, s, "x"); got[Purchase] != 2710 {
		t.Fatalf("purchase balance = %d, want 2710", got[Purchase])
	}
	_ = b1
}
