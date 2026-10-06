package billing

import (
	"errors"
	"reflect"
	"testing"
)

// Standard test config: grace 2 days, 0.1%/day late fee, cap 50% of
// principal, dunning thresholds at 5/10/15 overdue days.
func testCfg() Config {
	return Config{
		GraceDays:       2,
		RateNum:         1,
		RateDen:         1000,
		CapNum:          1,
		CapDen:          2,
		StageThresholds: [3]int64{5, 10, 15},
	}
}

func newService(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func codeOf(t *testing.T, err error) ErrCode {
	t.Helper()
	if err == nil {
		return 0
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error is not *billing.Error: %v", err)
	}
	return e.Code
}

func expectErr(t *testing.T, err error, code ErrCode) {
	t.Helper()
	if got := codeOf(t, err); got != code {
		t.Fatalf("expected error code %v, got %v (err=%v)", code, got, err)
	}
}

func addHousehold(t *testing.T, s *Service, id string, now int64) {
	t.Helper()
	must(t, s.AddHousehold(id, now))
}

func genBill(t *testing.T, s *Service, h, period string, principal, due, now int64) string {
	t.Helper()
	id, err := s.GenerateBill(h, period, principal, due, now)
	must(t, err)
	return id
}

func billView(t *testing.T, s *Service, h, bill string) BillView {
	t.Helper()
	v, err := s.BillView(h, bill)
	must(t, err)
	return v
}

func totalDue(t *testing.T, s *Service, h string, day int64) int64 {
	t.Helper()
	total, err := s.TotalDue(h, day)
	must(t, err)
	return total
}

func pay(t *testing.T, s *Service, h string, amount, now int64) *Receipt {
	t.Helper()
	r, err := s.Pay(h, amount, now)
	must(t, err)
	return r
}

func checkConservation(t *testing.T, s *Service, h string) {
	t.Helper()
	v, err := s.HouseholdView(h)
	must(t, err)
	if v.TotalPaid != v.AllocatedPrincipal+v.AllocatedLateFee+v.Prepay {
		t.Fatalf("conservation violated for %s: %+v", h, v)
	}
}

// The last grace day accrues nothing; the next day starts accrual.
func TestGraceLastDayAndNextDay(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	addHousehold(t, s, "HB", 0)
	ba := genBill(t, s, "HA", "2026-09", 1000, 10, 0) // grace through day 12
	genBill(t, s, "HB", "2026-09", 1000, 10, 0)

	// Paying off on the last grace day: no late fee.
	r := pay(t, s, "HA", 1000, 12)
	if len(r.Allocations) != 1 || r.Allocations[0].LateFee != 0 || r.Allocations[0].Principal != 1000 {
		t.Fatalf("last-grace-day receipt: %+v", r)
	}
	if v := billView(t, s, "HA", ba); !v.Closed || v.LateFee != 0 {
		t.Fatalf("bill should be closed with no late fee: %+v", v)
	}

	// The day after grace, one day of late fee: 1000 * 1/1000 = 1.
	if got := totalDue(t, s, "HB", 12); got != 1000 {
		t.Fatalf("due at last grace day = %d, want 1000", got)
	}
	if got := totalDue(t, s, "HB", 13); got != 1001 {
		t.Fatalf("due one day after grace = %d, want 1001", got)
	}
	r = pay(t, s, "HB", 1001, 13)
	if len(r.Allocations) != 1 || r.Allocations[0].LateFee != 1 || r.Allocations[0].Principal != 1000 {
		t.Fatalf("post-grace receipt should offset late fee first: %+v", r)
	}
	checkConservation(t, s, "HA")
	checkConservation(t, s, "HB")
}

// The late fee stops growing exactly when the cap is reached.
func TestLateFeeCapArrivalDay(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	b := genBill(t, s, "HA", "2026-09", 1000, 10, 0)
	// 1 unit/day from day 13, cap = 1000/2 = 500 -> reached on day 512.
	if got := totalDue(t, s, "HA", 511); got != 1499 {
		t.Fatalf("day before cap: due = %d, want 1499", got)
	}
	if got := totalDue(t, s, "HA", 512); got != 1500 {
		t.Fatalf("cap arrival day: due = %d, want 1500", got)
	}
	if got := totalDue(t, s, "HA", 900); got != 1500 {
		t.Fatalf("after cap: due = %d, want 1500 (capped)", got)
	}
	r := pay(t, s, "HA", 1500, 900)
	if r.Allocations[0].LateFee != 500 || r.Allocations[0].Principal != 1000 {
		t.Fatalf("capped receipt: %+v", r)
	}
	if v := billView(t, s, "HA", b); !v.Closed || v.LateFee != 500 {
		t.Fatalf("bill after capped payoff: %+v", v)
	}
	checkConservation(t, s, "HA")
}

// Sub-unit daily fractions are carried, not discarded, and survive a
// principal change.
func TestFractionalCarry(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	genBill(t, s, "HA", "2026-09", 500, 10, 0) // 0.5 units/day from day 13
	cases := []struct {
		day  int64
		want int64
	}{
		{13, 500}, // 0.5 accrued, 0 recorded
		{14, 501}, // 1.0 -> 1
		{15, 501}, // 1.5 -> 1
		{16, 502}, // 2.0 -> 2
	}
	for _, c := range cases {
		if got := totalDue(t, s, "HA", c.day); got != c.want {
			t.Fatalf("day %d: due = %d, want %d", c.day, got, c.want)
		}
	}
	// Pay 1 on day 13 (all to principal); the carried 0.5 must survive
	// the principal change: day 15 accrues 0.5 + 2*0.499 = 1.498 -> 1.
	pay(t, s, "HA", 1, 13)
	if got := totalDue(t, s, "HA", 15); got != 500 {
		t.Fatalf("day 15 after payment: due = %d, want 500", got)
	}
	checkConservation(t, s, "HA")
}

// One payment offsets bills in due-date order, late fee before
// principal within each bill, until it is exhausted.
func TestCrossBillAllocationOrder(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	b1 := genBill(t, s, "HA", "2026-08", 1000, 10, 0)
	b2 := genBill(t, s, "HA", "2026-09", 2000, 20, 0)

	// At day 25: b1 late = 13 (days 13..25), b2 late = 6 (days 23..25).
	r := pay(t, s, "HA", 1519, 25)
	want := []Allocation{
		{BillID: b1, LateFee: 13, Principal: 1000},
		{BillID: b2, LateFee: 6, Principal: 500},
	}
	if !reflect.DeepEqual(r.Allocations, want) {
		t.Fatalf("allocations = %+v, want %+v", r.Allocations, want)
	}
	if r.PrepayAdded != 0 {
		t.Fatalf("payment should be exactly exhausted, prepay = %d", r.PrepayAdded)
	}
	if v := billView(t, s, "HA", b1); !v.Closed {
		t.Fatalf("b1 should be closed: %+v", v)
	}
	if v := billView(t, s, "HA", b2); v.Closed || v.PrincipalPaid != 500 || v.LateFeePaid != 6 {
		t.Fatalf("b2 partially offset: %+v", v)
	}
	checkConservation(t, s, "HA")
}

// A payment with no open bills becomes prepayment, which is
// automatically applied to bills generated later.
func TestPrepayCreationAndAutoUse(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	r := pay(t, s, "HA", 500, 0)
	if r.PrepayAdded != 500 || len(r.Allocations) != 0 {
		t.Fatalf("payment without bills should become prepay: %+v", r)
	}

	b1 := genBill(t, s, "HA", "2026-08", 300, 10, 0)
	if v := billView(t, s, "HA", b1); !v.Closed || v.PrincipalPaid != 300 {
		t.Fatalf("prepay should fully cover the new bill: %+v", v)
	}
	b2 := genBill(t, s, "HA", "2026-09", 500, 10, 0)
	if v := billView(t, s, "HA", b2); v.Closed || v.PrincipalPaid != 200 {
		t.Fatalf("remaining prepay 200 should offset the new bill: %+v", v)
	}
	if got := totalDue(t, s, "HA", 0); got != 300 {
		t.Fatalf("due after prepay auto-use = %d, want 300", got)
	}
	hv, err := s.HouseholdView("HA")
	must(t, err)
	if hv.Prepay != 0 || hv.TotalPaid != 500 || hv.AllocatedPrincipal != 500 {
		t.Fatalf("household view: %+v", hv)
	}
	checkConservation(t, s, "HA")
}

// A disputed bill is skipped by payments, its late fee and stage are
// frozen, and accrual resumes (without back-counting) after the ruling.
func TestDisputeSkipsPaymentAndResumes(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	b1 := genBill(t, s, "HA", "2026-08", 1000, 10, 0)
	b2 := genBill(t, s, "HA", "2026-09", 500, 10, 0)

	must(t, s.Dispute("HA", b1, 20)) // b1 late frozen at 7 (days 13..19)
	r := pay(t, s, "HA", 600, 25)
	// b1 skipped; b2 late = 500*12/1000 = 6, then principal 500.
	want := []Allocation{{BillID: b2, LateFee: 6, Principal: 500}}
	if !reflect.DeepEqual(r.Allocations, want) {
		t.Fatalf("allocations = %+v, want %+v", r.Allocations, want)
	}
	if r.PrepayAdded != 94 {
		t.Fatalf("leftover should become prepay: %+v", r)
	}
	if v := billView(t, s, "HA", b1); !v.Disputed || v.LateFee != 7 || v.Stage != StageNone {
		t.Fatalf("disputed bill frozen: %+v", v)
	}

	must(t, s.ResolveDispute("HA", b1, 30, false, 0)) // upheld, 10 disputed days
	r = pay(t, s, "HA", 2000, 35)
	// Accrual resumes on day 30: days 30..35 add 6 -> late 13 total.
	want = []Allocation{{BillID: b1, LateFee: 13, Principal: 1000}}
	if !reflect.DeepEqual(r.Allocations, want) {
		t.Fatalf("allocations after ruling = %+v, want %+v", r.Allocations, want)
	}
	if v := billView(t, s, "HA", b1); !v.Closed || !v.Ruled || v.DisputedDays != 10 || v.LateFee != 13 {
		t.Fatalf("b1 after ruling and payoff: %+v", v)
	}
	checkConservation(t, s, "HA")
}

// A ruling that reduces the principal rebases the bill; already-paid
// amounts are not refunded but the overpaid excess becomes prepayment.
func TestRulingReductionRecompute(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	addHousehold(t, s, "HB", 0)

	// Overpaid principal: excess moves to prepay on reduction.
	b1 := genBill(t, s, "HA", "2026-08", 1000, 10, 0)
	pay(t, s, "HA", 600, 12) // inside grace, all to principal
	must(t, s.Dispute("HA", b1, 15))
	must(t, s.ResolveDispute("HA", b1, 20, true, 400))
	if v := billView(t, s, "HA", b1); v.Principal != 400 || v.PrincipalPaid != 400 || !v.Closed {
		t.Fatalf("reduced bill: %+v", v)
	}
	hv, err := s.HouseholdView("HA")
	must(t, err)
	if hv.Prepay != 200 || hv.TotalPaid != 600 || hv.AllocatedPrincipal != 400 {
		t.Fatalf("excess should become prepay, not refund: %+v", hv)
	}
	checkConservation(t, s, "HA")

	// Reduced principal becomes the new accrual basis. The clock is
	// already at day 20 from the HA operations above.
	b2 := genBill(t, s, "HB", "2026-08", 1000, 10, 20)
	must(t, s.Dispute("HB", b2, 21)) // late fee 8 accrued (days 13..20)
	must(t, s.ResolveDispute("HB", b2, 26, true, 500))
	r := pay(t, s, "HB", 1000, 31)
	// Accrual on the reduced 500 for days 26..31: 6 * 0.5 = 3; total 11.
	want := []Allocation{{BillID: b2, LateFee: 11, Principal: 500}}
	if !reflect.DeepEqual(r.Allocations, want) {
		t.Fatalf("allocations = %+v, want %+v", r.Allocations, want)
	}
	checkConservation(t, s, "HB")
}

// Waivers are bounded by the unpaid late fee; the unpaid principal
// keeps accruing new late fees afterwards.
func TestWaiverBoundary(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	b := genBill(t, s, "HA", "2026-08", 1000, 10, 0)

	// At day 22 the late fee is 10 (days 13..22).
	expectErr(t, s.Waive("HA", b, 0, 22), ErrAmountOutOfRange)
	expectErr(t, s.Waive("HA", b, -3, 22), ErrAmountOutOfRange)
	expectErr(t, s.Waive("HA", b, 11, 22), ErrAmountOutOfRange)
	must(t, s.Waive("HA", b, 10, 22)) // exact boundary: waiving everything
	if v := billView(t, s, "HA", b); v.LateFee != 10 || v.LateFeeWaived != 10 {
		t.Fatalf("after waiver: %+v", v)
	}
	expectErr(t, s.Waive("HA", b, 1, 22), ErrAmountOutOfRange)

	// The still-unpaid principal accrues new late fees: by day 32 ten
	// more units, of which exactly the new 10 are waivable/due.
	if got := totalDue(t, s, "HA", 32); got != 1010 {
		t.Fatalf("due at day 32 = %d, want 1010", got)
	}
	must(t, s.Waive("HA", b, 10, 32))
	r := pay(t, s, "HA", 1000, 32)
	if len(r.Allocations) != 1 || r.Allocations[0].Principal != 1000 || r.Allocations[0].LateFee != 0 {
		t.Fatalf("payoff after waivers: %+v", r)
	}
	if v := billView(t, s, "HA", b); !v.Closed {
		t.Fatalf("bill should be closed: %+v", v)
	}
	expectErr(t, s.Waive("HA", b, 1, 32), ErrInvalidState) // closed bill
	checkConservation(t, s, "HA")
}

// Stages advance exactly when the overdue-day count reaches a
// threshold, and never regress.
func TestStageThresholdsExact(t *testing.T) {
	s := newService(t, testCfg()) // thresholds 5 / 10 / 15
	addHousehold(t, s, "HA", 0)
	b := genBill(t, s, "HA", "2026-08", 100000, 10, 0)

	steps := []struct {
		now  int64
		want int
	}{
		{14, StageNone},     // overdue 4
		{15, StageReminder}, // overdue 5, exactly threshold 1
		{19, StageReminder}, // overdue 9
		{20, StageWarning},  // overdue 10, exactly threshold 2
		{24, StageWarning},  // overdue 14
		{25, StageFinal},    // overdue 15, exactly threshold 3
	}
	for _, st := range steps {
		pay(t, s, "HA", 1, st.now) // any accepted op advances stages
		if got := billView(t, s, "HA", b).Stage; got != st.want {
			t.Fatalf("day %d: stage = %d, want %d", st.now, got, st.want)
		}
	}
	checkConservation(t, s, "HA")
}

// A stage-3 bill is only offset by a payment covering it in full;
// otherwise it is skipped (not an error) and later bills are served.
func TestStageFinalFullPaymentOnly(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	b1 := genBill(t, s, "HA", "2026-07", 1000, 0, 0)
	b2 := genBill(t, s, "HA", "2026-08", 500, 100, 0)

	// Day 20: b1 late = 18, overdue 20 -> stage 3, need = 1018.
	r := pay(t, s, "HA", 1, 20)
	want := []Allocation{{BillID: b2, LateFee: 0, Principal: 1}}
	if !reflect.DeepEqual(r.Allocations, want) {
		t.Fatalf("stage-3 bill should be skipped: %+v", r.Allocations)
	}
	if v := billView(t, s, "HA", b1); v.Stage != StageFinal || v.PrincipalPaid != 0 {
		t.Fatalf("b1 untouched at stage 3: %+v", v)
	}

	// 1017 is one short of the full need: skipped again, serves b2.
	r = pay(t, s, "HA", 1017, 20)
	want = []Allocation{{BillID: b2, LateFee: 0, Principal: 499}}
	if !reflect.DeepEqual(r.Allocations, want) {
		t.Fatalf("partial payment must skip stage-3 bill: %+v", r.Allocations)
	}

	// Exactly the full need offsets b1 (late fee first, then principal).
	r = pay(t, s, "HA", 1018, 20)
	want = []Allocation{{BillID: b1, LateFee: 18, Principal: 1000}}
	if !reflect.DeepEqual(r.Allocations, want) {
		t.Fatalf("full payment offsets stage-3 bill: %+v", r.Allocations)
	}
	if got := totalDue(t, s, "HA", 20); got != 0 {
		t.Fatalf("all bills closed, due = %d, want 0", got)
	}
	checkConservation(t, s, "HA")
}

// Errors are reported in a fixed precedence: invalid param, clock
// rollback, not found, invalid state, amount out of range.
func TestRejectionOrder(t *testing.T) {
	bad := testCfg()
	bad.StageThresholds = [3]int64{5, 5, 9}
	_, err := NewService(bad)
	expectErr(t, err, ErrInvalidParam)
	bad = testCfg()
	bad.RateDen = 0
	_, err = NewService(bad)
	expectErr(t, err, ErrInvalidParam)

	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 10) // clock at 10

	// Invalid param beats clock rollback.
	expectErr(t, s.Pay2Err("", 100, 9), ErrInvalidParam)
	// Clock rollback beats not-found and amount.
	expectErr(t, s.Pay2Err("ghost", 0, 9), ErrClockRollback)
	// Not-found beats amount.
	expectErr(t, s.Pay2Err("ghost", 0, 10), ErrNotFound)
	// Amount last.
	expectErr(t, s.Pay2Err("HA", 0, 10), ErrAmountOutOfRange)
	expectErr(t, s.Pay2Err("HA", -7, 10), ErrAmountOutOfRange)

	expectErr(t, s.AddHousehold("HA", 10), ErrInvalidState)
	expectErr(t, s.AddHousehold("", 10), ErrInvalidParam)
	_, err = s.GenerateBill("HA", "p", 0, 10, 10)
	expectErr(t, err, ErrInvalidParam)
	_, err = s.GenerateBill("ghost", "p", 100, 10, 10)
	expectErr(t, err, ErrNotFound)

	b := genBill(t, s, "HA", "2026-08", 1000, 10, 10)
	expectErr(t, s.Dispute("HA", "nope", 10), ErrNotFound)
	must(t, s.Dispute("HA", b, 10))
	expectErr(t, s.Dispute("HA", b, 10), ErrInvalidState) // duplicate dispute
	expectErr(t, s.ResolveDispute("HA", b, 10, true, 2000), ErrInvalidParam)
	expectErr(t, s.ResolveDispute("HA", b, 10, true, -1), ErrInvalidParam)
	must(t, s.ResolveDispute("HA", b, 11, false, 0))
	expectErr(t, s.ResolveDispute("HA", b, 11, false, 0), ErrInvalidState) // re-ruling

	// Late fee is 1 on day 11 (accrual resumed on the ruling day).
	expectErr(t, s.Waive("HA", b, 2, 11), ErrAmountOutOfRange)
	pay(t, s, "HA", 1000, 20)                             // late 10, principal 990
	pay(t, s, "HA", 10, 20)                               // principal 10 -> closed
	expectErr(t, s.Dispute("HA", b, 20), ErrInvalidState) // closed bill
	expectErr(t, s.Waive("HA", b, 1, 20), ErrInvalidState)

	_, err = s.TotalDue("HA", 5)
	expectErr(t, err, ErrInvalidParam) // query day before now
	_, err = s.TotalDue("ghost", 20)
	expectErr(t, err, ErrNotFound)
	checkConservation(t, s, "HA")
}

// Pay2Err is a test helper keeping the rejection-order assertions terse.
func (s *Service) Pay2Err(householdID string, amount, now int64) error {
	_, err := s.Pay(householdID, amount, now)
	return err
}

// A rejected operation changes nothing: bills, payments, prepay and the
// clock are all untouched.
func TestRejectedOpLeavesNoTrace(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	b1 := genBill(t, s, "HA", "2026-08", 1000, 10, 0)
	b2 := genBill(t, s, "HA", "2026-09", 500, 20, 0)
	pay(t, s, "HA", 100, 15)

	snapshot := func() []any {
		hv, err := s.HouseholdView("HA")
		must(t, err)
		due, err := s.TotalDue("HA", 40)
		must(t, err)
		return []any{hv, billView(t, s, "HA", b1), billView(t, s, "HA", b2), due}
	}
	before := snapshot()

	rejected := []error{
		s.Pay2Err("HA", 0, 15),                   // zero payment
		s.Pay2Err("ghost", 1, 15),                // unknown household
		s.Pay2Err("HA", 1, 14),                   // clock rollback
		s.Waive("HA", b1, 99999, 15),             // waiver above unpaid late fee
		s.Dispute("HA", "nope", 15),              // unknown bill
		s.ResolveDispute("HA", b1, 15, false, 0), // no open dispute
		s.AddHousehold("HA", 15),                 // duplicate household
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("rejected op %d unexpectedly succeeded", i)
		}
	}
	if _, err := s.GenerateBill("HA", "p", -1, 0, 15); err == nil {
		t.Fatalf("negative principal should be rejected")
	}

	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected ops left traces:\nbefore=%v\nafter =%v", before, after)
	}
	// The clock was not advanced by rejected ops: now 15 still accepted.
	pay(t, s, "HA", 1, 15)
	checkConservation(t, s, "HA")
}

// TotalDue visits exactly the open bills, however many bills the
// household has closed in its history.
func TestQueryCostIndependentOfHistory(t *testing.T) {
	s := newService(t, testCfg())
	addHousehold(t, s, "HA", 0)
	addHousehold(t, s, "HB", 0)

	for i := 0; i < 300; i++ {
		genBill(t, s, "HA", "old", 10, 0, 0)
	}
	pay(t, s, "HA", 3000, 1) // closes all 300 historical bills
	for i := 0; i < 3; i++ {
		genBill(t, s, "HA", "new", 100, 5, 1)
		genBill(t, s, "HB", "new", 100, 5, 1)
	}

	if _, err := s.TotalDue("HA", 10); err != nil {
		t.Fatal(err)
	}
	visitsA := s.QueryVisits()
	if _, err := s.TotalDue("HB", 10); err != nil {
		t.Fatal(err)
	}
	visitsB := s.QueryVisits()
	if visitsA != 3 || visitsB != 3 {
		t.Fatalf("query visits = %d (300 closed) and %d (no history), want 3 and 3", visitsA, visitsB)
	}
	hv, err := s.HouseholdView("HA")
	must(t, err)
	if hv.ClosedBills != 300 || hv.OpenBills != 3 {
		t.Fatalf("household view: %+v", hv)
	}
	checkConservation(t, s, "HA")
}
