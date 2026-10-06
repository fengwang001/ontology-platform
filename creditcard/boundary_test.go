package creditcard

import (
	"errors"
	"reflect"
	"testing"
)

// Interest below one currency unit is carried as a remainder into the
// next period instead of being lost.
func TestInterestRemainderCarryover(t *testing.T) {
	s := NewService()
	p := zeroRates(0, 0, 0, 0)
	p.RateBps = [NumCategories]int64{10000, 0, 0} // cash: 100% p.a.
	mustCreate(t, s, "x", p, 0)
	mustCharge(t, s, "x", Cash, 1, 0)

	// Period 1: 100 days * 1 * 10000 = 1_000_000 < 3_650_000 -> 0 interest.
	b1 := mustBill(t, s, "x", 100)
	if b1.Interest[Cash] != 0 {
		t.Fatalf("bill1 interest = %d, want 0", b1.Interest[Cash])
	}
	// Period 2: 300 days * 1 * 10000 = 3_000_000 + carried 1_000_000
	// = 4_000_000 -> interest 1, remainder 350_000 carried on.
	b2 := mustBill(t, s, "x", 400)
	if b2.Interest[Cash] != 1 {
		t.Fatalf("bill2 interest = %d, want 1 (remainder must carry over)", b2.Interest[Cash])
	}
	// Period 3: 1 day * 2 * 10000 = 20_000 + 350_000 < 3_650_000 -> 0.
	b3 := mustBill(t, s, "x", 401)
	if b3.Interest[Cash] != 0 {
		t.Fatalf("bill3 interest = %d, want 0", b3.Interest[Cash])
	}
	if got := balancesOf(t, s, "x"); got[Cash] != 2 {
		t.Fatalf("cash balance = %d, want 2", got[Cash])
	}
}

// One payment can span the minimum-payment part (fixed category order)
// and the excess part (rate order).
func TestPaymentSpanningMinAndExcess(t *testing.T) {
	s := NewService()
	p := zeroRates(5, 1000, 0, 0)
	p.RateBps = [NumCategories]int64{300, 200, 100}
	mustCreate(t, s, "x", p, 0)
	mustCharge(t, s, "x", Cash, 1000, 0)
	mustCharge(t, s, "x", Installment, 1000, 0)
	mustCharge(t, s, "x", Purchase, 1000, 0)

	b1 := mustBill(t, s, "x", 1) // total 3000, min = ceil(3000*10%) = 300
	if b1.Total != 3000 || b1.MinPayment != 300 {
		t.Fatalf("unexpected bill1: %+v", b1)
	}

	// 500 = 300 min part (fixed order: cash first) + 200 excess (rate
	// order: cash has the highest rate, so cash again).
	rec := mustPay(t, s, "x", 500, 2)
	if rec.Alloc != [NumCategories]int64{500, 0, 0} || rec.Overpayment != 0 {
		t.Fatalf("payment1 alloc = %+v, want {500,0,0}", rec)
	}
	if got := balancesOf(t, s, "x"); got != [NumCategories]int64{500, 1000, 1000} {
		t.Fatalf("balances = %v, want {500 1000 1000}", got)
	}

	// Minimum already covered: everything follows rate order
	// cash(300) > installment(200) > purchase(100).
	rec = mustPay(t, s, "x", 2000, 2)
	if rec.Alloc != [NumCategories]int64{500, 1000, 500} {
		t.Fatalf("payment2 alloc = %+v, want {500,1000,500}", rec)
	}
	if got := balancesOf(t, s, "x"); got != [NumCategories]int64{0, 0, 500} {
		t.Fatalf("balances = %v, want {0 0 500}", got)
	}
}

// Overpayment is recorded and absorbed by later charges before any new
// balance forms.
func TestOverpaymentOffsetsCharges(t *testing.T) {
	s := NewService()
	p := zeroRates(5, 1000, 0, 0)
	mustCreate(t, s, "x", p, 0)
	mustCharge(t, s, "x", Purchase, 100, 0)
	mustBill(t, s, "x", 1) // total 100, min 10

	rec := mustPay(t, s, "x", 500, 2)
	if rec.Alloc != [NumCategories]int64{0, 0, 100} || rec.Overpayment != 400 {
		t.Fatalf("alloc = %+v, want {0,0,100} + 400 overpayment", rec)
	}
	if got := overOf(t, s, "x"); got != 400 {
		t.Fatalf("overpayment = %d, want 400", got)
	}

	mustCharge(t, s, "x", Purchase, 250, 3) // fully absorbed
	if got := overOf(t, s, "x"); got != 150 {
		t.Fatalf("overpayment = %d, want 150", got)
	}
	if got := balancesOf(t, s, "x"); got != [NumCategories]int64{0, 0, 0} {
		t.Fatalf("balances = %v, want all zero", got)
	}

	mustCharge(t, s, "x", Cash, 200, 3) // 150 absorbed, 50 becomes balance
	if got := overOf(t, s, "x"); got != 0 {
		t.Fatalf("overpayment = %d, want 0", got)
	}
	if got := balancesOf(t, s, "x"); got != [NumCategories]int64{50, 0, 0} {
		t.Fatalf("balances = %v, want {50 0 0}", got)
	}
}

// Rejected operations must leave state and clock untouched, and errors
// follow the priority invalid > clock > not-found > too-early.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := NewService()
	p := zeroRates(5, 1000, 0, 0)
	mustCreate(t, s, "x", p, 5)
	mustCharge(t, s, "x", Purchase, 100, 5)
	mustBill(t, s, "x", 6) // due 11; clock = 6

	type state struct {
		bal  [NumCategories]int64
		over int64
		bill []Bill
		pay  []PaymentRecord
	}
	snap := func() state {
		return state{balancesOf(t, s, "x"), overOf(t, s, "x"), billsOf(t, s, "x"), paymentsOf(t, s, "x")}
	}
	before := snap()

	check := func(name string, err error, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err = %v, want %v", name, err, want)
		}
		if got := snap(); !reflect.DeepEqual(got, before) {
			t.Fatalf("%s: rejected op changed state: %+v", name, got)
		}
	}

	check("zero charge", s.Charge("x", Purchase, 0, 6), ErrInvalidParam)
	check("negative charge", s.Charge("x", Purchase, -1, 6), ErrInvalidParam)
	check("bad category", s.Charge("x", Category(9), 1, 6), ErrInvalidParam)
	check("zero pay", func() error { _, err := s.Pay("x", 0, 6); return err }(), ErrInvalidParam)
	check("invalid beats clock+notfound",
		func() error { _, err := s.Pay("nope", 0, 0); return err }(), ErrInvalidParam)
	check("clock rollback", func() error { _, err := s.Pay("x", 1, 5); return err }(), ErrClockRollback)
	check("clock beats notfound",
		func() error { _, err := s.Pay("nope", 1, 5); return err }(), ErrClockRollback)
	check("not found", s.Charge("nope", Cash, 1, 6), ErrAccountNotFound)
	check("billing too early", func() error { _, err := s.Bill("x", 6); return err }(), ErrBillingTooEarly)
	check("billing too early on due date",
		func() error { _, err := s.Bill("x", 11); return err }(), ErrBillingTooEarly)
	check("clock beats too-early", func() error { _, err := s.Bill("x", 5); return err }(), ErrClockRollback)
	check("bill not found", func() error { _, err := s.Bill("nope", 6); return err }(), ErrAccountNotFound)
	check("create duplicate", s.CreateAccount("x", p, 6), ErrAccountExists)
	check("create invalid params", s.CreateAccount("y", Params{GraceDays: -1}, 6), ErrInvalidParam)

	// Clock was not moved by any rejection: now == 6 is still accepted.
	if err := s.Charge("x", Purchase, 1, 6); err != nil {
		t.Fatalf("op at last accepted day must succeed: %v", err)
	}
}

// The same operation sequence replayed on a fresh service must produce
// identical bills, payment records and balances.
func TestReplayDeterminism(t *testing.T) {
	p := zeroRates(3, 2000, 10, 30)
	p.RateBps = [NumCategories]int64{1800, 2400, 3600}
	run := func() ([]Bill, []PaymentRecord, [NumCategories]int64, int64) {
		s := NewService()
		mustCreate(t, s, "x", p, 0)
		mustCharge(t, s, "x", Purchase, 777, 0)
		mustCharge(t, s, "x", Cash, 333, 1)
		mustBill(t, s, "x", 5)
		mustPay(t, s, "x", 100, 6)
		mustCharge(t, s, "x", Installment, 500, 7)
		mustPay(t, s, "x", 50, 8) // after due date 8? due = 5+3 = 8, inclusive
		mustBill(t, s, "x", 12)
		mustPay(t, s, "x", 2000, 13)
		mustBill(t, s, "x", 20)
		return billsOf(t, s, "x"), paymentsOf(t, s, "x"), balancesOf(t, s, "x"), overOf(t, s, "x")
	}
	b1, p1, bal1, o1 := run()
	b2, p2, bal2, o2 := run()
	if !reflect.DeepEqual(b1, b2) || !reflect.DeepEqual(p1, p2) || bal1 != bal2 || o1 != o2 {
		t.Fatalf("replay mismatch:\nbills %v vs %v\npays %v vs %v\nbal %v vs %v\nover %v vs %v",
			b1, b2, p1, p2, bal1, bal2, o1, o2)
	}
}

// Billing cost must not grow with the number of historical periods or
// transactions. Allocations per billing are a deterministic proxy.
func TestBillCostIndependentOfHistory(t *testing.T) {
	p := zeroRates(0, 1000, 0, 10)
	p.RateBps = [NumCategories]int64{999, 888, 777}

	build := func(periods int) (*Service, string) {
		s := NewService()
		id := "x"
		mustCreate(t, s, id, p, 0)
		day := int64(0)
		for i := 0; i < periods; i++ {
			day++
			mustCharge(t, s, id, Cash, 1000, day)
			mustCharge(t, s, id, Purchase, 500, day)
			mustPay(t, s, id, 100, day)
			mustBill(t, s, id, day)
		}
		return s, id
	}

	allocs := func(s *Service, id string, startDay int64) float64 {
		day := startDay
		return testing.AllocsPerRun(50, func() {
			day++
			if _, err := s.Bill(id, day); err != nil {
				t.Fatalf("Bill: %v", err)
			}
		})
	}

	sSmall, idSmall := build(5)
	sLarge, idLarge := build(2000)
	aSmall := allocs(sSmall, idSmall, 100)
	aLarge := allocs(sLarge, idLarge, 10000)
	t.Logf("allocs per billing: 5 periods -> %.2f, 2000 periods -> %.2f", aSmall, aLarge)
	if aLarge > aSmall+1 {
		t.Fatalf("billing cost grows with history: %.2f vs %.2f allocs", aSmall, aLarge)
	}
}
