package billing

import (
	"testing"
)

func testConfig() Config {
	return Config{
		GraceDays: 3,
		RateNum:   1, RateDen: 10, // 10% of unpaid principal per day
		CapNum: 1, CapDen: 2, // late fee capped at 50% of principal
		Thresholds: [3]int{5, 10, 15},
	}
}

func mustNew(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s
}

func mustGenerate(t *testing.T, s *Service, now int, hh, bill string, principal int64, due int) {
	t.Helper()
	if err := s.GenerateBill(now, hh, bill, principal, due); err != nil {
		t.Fatalf("GenerateBill(%s/%s): %v", hh, bill, err)
	}
}

func mustTotalDue(t *testing.T, s *Service, now int, hh string) int64 {
	t.Helper()
	due, err := s.TotalDue(now, hh)
	if err != nil {
		t.Fatalf("TotalDue(%s): %v", hh, err)
	}
	return due
}

func mustStage(t *testing.T, s *Service, now int, hh, bill string) int {
	t.Helper()
	st, err := s.Status(now, hh, bill)
	if err != nil {
		t.Fatalf("Status(%s/%s): %v", hh, bill, err)
	}
	return st.Stage
}

func checkErrCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	be, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if be.Code != want {
		t.Fatalf("expected error code %v, got %v (%v)", want, be.Code, err)
	}
}

// Grace: the last grace day is still grace; accrual starts the next day.
func TestGraceLastDayAndNextDay(t *testing.T) {
	s := mustNew(t, testConfig())
	// due day 10, grace 3 -> grace covers 11,12,13; accrual from day 14.
	mustGenerate(t, s, 0, "h1", "b1", 100, 10)
	if got := mustTotalDue(t, s, 13, "h1"); got != 100 {
		t.Fatalf("last grace day: total due = %d, want 100", got)
	}
	res, err := s.Pay(13, "h1", 100)
	if err != nil {
		t.Fatalf("pay on last grace day: %v", err)
	}
	if len(res.Applications) != 1 || res.Applications[0].ToLate != 0 || res.Applications[0].ToPrincipal != 100 {
		t.Fatalf("grace payment applied late fee: %+v", res.Applications)
	}
	st, err := s.Status(13, "h1", "b1")
	if err != nil || !st.Closed || st.AccruedLate != 0 {
		t.Fatalf("bill should be closed with no late fee, got %+v, err=%v", st, err)
	}

	mustGenerate(t, s, 13, "h2", "b1", 100, 10)
	if got := mustTotalDue(t, s, 14, "h2"); got != 110 {
		t.Fatalf("day after grace: total due = %d, want 110", got)
	}
	if got := mustTotalDue(t, s, 15, "h2"); got != 120 {
		t.Fatalf("two days after grace: total due = %d, want 120", got)
	}
}

// Cap: 10%/day on principal 100, cap 50% -> 10,20,30,40,50 then frozen.
func TestLateFeeCapArrivalDay(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 0, "h1", "b1", 100, 0)
	// accrual starts day 4 (due 0 + grace 3 + 1).
	want := map[int]int64{4: 110, 5: 120, 6: 130, 7: 140, 8: 150}
	for day := 4; day <= 30; day++ {
		got := mustTotalDue(t, s, day, "h1")
		w := int64(150) // capped from day 8 on
		if d, ok := want[day]; ok {
			w = d
		}
		if got != w {
			t.Fatalf("day %d: total due = %d, want %d", day, got, w)
		}
	}
	st, err := s.Status(30, "h1", "b1")
	if err != nil {
		t.Fatal(err)
	}
	if st.AccruedLate != 50 {
		t.Fatalf("accrued late = %d, want capped 50", st.AccruedLate)
	}
}

// Fractional carryover: 1/2 per day on principal 7 -> 3, 7, 10, 14.
func TestFractionalCarryover(t *testing.T) {
	cfg := testConfig()
	cfg.RateNum, cfg.RateDen = 1, 2
	cfg.CapNum, cfg.CapDen = 10, 1 // no effective cap
	s := mustNew(t, cfg)
	mustGenerate(t, s, 0, "h1", "b1", 7, 0)
	want := []int64{0, 0, 0, 0, 3, 7, 10, 14} // day -> accrued late
	for day := 0; day <= 7; day++ {
		st, err := s.Status(day, "h1", "b1")
		if err != nil {
			t.Fatal(err)
		}
		if st.AccruedLate != want[day] {
			t.Fatalf("day %d: accrued late = %d, want %d", day, st.AccruedLate, want[day])
		}
	}
}

// Cross-bill application order: earliest due first, late fee before
// principal within a bill, payment exhausted exactly.
func TestCrossBillApplicationOrder(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 0, "h1", "b-late", 100, 8) // due later
	mustGenerate(t, s, 0, "h1", "b-early", 50, 0) // due first, accrues from day 4
	// At day 6: b-early late = 3 days * 5 = 15, outstanding 65; b-late clean.
	res, err := s.Pay(6, "h1", 65+40)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applications) != 2 {
		t.Fatalf("expected 2 applications, got %+v", res.Applications)
	}
	if res.Applications[0].BillID != "b-early" || res.Applications[0].ToLate != 15 || res.Applications[0].ToPrincipal != 50 {
		t.Fatalf("first application wrong: %+v", res.Applications[0])
	}
	if res.Applications[1].BillID != "b-late" || res.Applications[1].ToLate != 0 || res.Applications[1].ToPrincipal != 40 {
		t.Fatalf("second application wrong: %+v", res.Applications[1])
	}
	if res.Prepaid != 0 {
		t.Fatalf("payment should be exhausted, prepaid = %d", res.Prepaid)
	}
	st, _ := s.Status(6, "h1", "b-early")
	if !st.Closed {
		t.Fatalf("b-early should be closed: %+v", st)
	}
	if got := mustTotalDue(t, s, 6, "h1"); got != 60 {
		t.Fatalf("remaining due = %d, want 60", got)
	}
}

// Prepay: leftover with no payable bills becomes prepay and is
// automatically applied to bills generated later.
func TestPrepayCreationAndAutoUse(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 0, "h1", "b1", 30, 0)
	res, err := s.Pay(1, "h1", 50) // overpay within grace
	if err != nil {
		t.Fatal(err)
	}
	if res.Prepaid != 20 {
		t.Fatalf("prepaid = %d, want 20", res.Prepaid)
	}
	mustGenerate(t, s, 2, "h1", "b2", 15, 2)
	st, err := s.Status(2, "h1", "b2")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Closed || st.PaidPrincipal != 15 {
		t.Fatalf("new bill should be fully covered by prepay: %+v", st)
	}
	v, err := s.View(2, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if v.Prepay != 5 {
		t.Fatalf("prepay balance = %d, want 5", v.Prepay)
	}
	// Conservation: total paid = applied principal + applied late + prepay.
	var appliedP, appliedL int64
	for _, bs := range v.Bills {
		appliedP += bs.PaidPrincipal
		appliedL += bs.PaidLate
	}
	if v.TotalPaid != appliedP+appliedL+v.Prepay {
		t.Fatalf("conservation violated: paid=%d principal=%d late=%d prepay=%d",
			v.TotalPaid, appliedP, appliedL, v.Prepay)
	}
}

// Dispute: payments skip the disputed bill, accrual stops during the
// dispute, dispute days are not back-filled, accrual resumes at ruling.
func TestDisputeSkipAndResume(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 0, "h1", "b1", 100, 0) // accrues from day 4
	mustGenerate(t, s, 0, "h1", "b2", 40, 20)
	// Day 6: b1 accrued 2 days * 10 = 20 (days 4,5). Dispute b1.
	if err := s.Dispute(6, "h1", "b1"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Status(6, "h1", "b1")
	if st.AccruedLate != 20 || !st.Disputed {
		t.Fatalf("at dispute: %+v", st)
	}
	// Payment skips b1 and goes to b2.
	res, err := s.Pay(10, "h1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applications) != 1 || res.Applications[0].BillID != "b2" || res.Applications[0].ToPrincipal != 40 {
		t.Fatalf("payment should skip disputed b1: %+v", res.Applications)
	}
	if res.Prepaid != 10 {
		t.Fatalf("leftover should become prepay: %+v", res)
	}
	// Accrual frozen while disputed.
	st, _ = s.Status(12, "h1", "b1")
	if st.AccruedLate != 20 {
		t.Fatalf("accrual should be frozen during dispute: %+v", st)
	}
	// Resolve (uphold) at day 12: days 6..11 excluded, resume from day 12.
	if err := s.ResolveDispute(12, "h1", "b1", 100); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Status(12, "h1", "b1")
	if st.AccruedLate != 30 || st.DisputedDays != 6 { // day 12 accrues again
		t.Fatalf("at ruling: %+v", st)
	}
	st, _ = s.Status(14, "h1", "b1")
	if st.AccruedLate != 50 { // 20 + 3 days * 10 (days 12,13,14)
		t.Fatalf("accrual should resume from ruling day: %+v", st)
	}
	// A bill can be disputed only once.
	checkErrCode(t, s.Dispute(15, "h1", "b1"), ErrInvalidState)
}

// Ruling reduction: base and cap recomputed on the reduced principal;
// pre-ruling payments are not refunded.
func TestResolveReductionRecompute(t *testing.T) {
	s := mustNew(t, testConfig()) // 10%/day, cap 50%
	mustGenerate(t, s, 0, "h1", "b1", 100, 0)
	if _, err := s.Pay(4, "h1", 70); err != nil { // 10 late + 60 principal
		t.Fatal(err)
	}
	if err := s.Dispute(5, "h1", "b1"); err != nil {
		t.Fatal(err)
	}
	// Reduce principal to 30; already paid 60 -> principal fully covered,
	// no refund. Late fee accrued so far: 10 (day 4 only).
	if err := s.ResolveDispute(8, "h1", "b1", 30); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Status(8, "h1", "b1")
	if st.Principal != 30 || st.PaidPrincipal != 60 {
		t.Fatalf("after reduction: %+v", st)
	}
	if got := mustTotalDue(t, s, 8, "h1"); got != 0 {
		t.Fatalf("total due after reduction = %d, want 0 (late fee already paid)", got)
	}
	// Unpaid principal is 0, so no new late fee accrues; bill is closed.
	st, _ = s.Status(20, "h1", "b1")
	if !st.Closed || st.AccruedLate != 10 {
		t.Fatalf("bill should be closed with accrued late 10: %+v", st)
	}

	// Second case: reduction lowers the accrual base and cap.
	mustGenerate(t, s, 20, "h2", "b1", 80, 20) // accrues from day 24
	if err := s.Dispute(24, "h2", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveDispute(26, "h2", "b1", 40); err != nil {
		t.Fatal(err)
	}
	// Resume at day 26 on base 40: 4/day, cap 20.
	st, _ = s.Status(27, "h2", "b1")
	if st.AccruedLate != 8 {
		t.Fatalf("day 27 accrued = %d, want 8 (2 days on reduced base 40)", st.AccruedLate)
	}
	st, _ = s.Status(40, "h2", "b1")
	if st.AccruedLate != 20 {
		t.Fatalf("accrued = %d, want capped 20 (50%% of reduced 40)", st.AccruedLate)
	}
}

// Waiver: only against accrued late fees, bounded by the unpaid amount.
func TestWaiveBoundary(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 0, "h1", "b1", 100, 0)                    // 10/day from day 4
	checkErrCode(t, s.Waive(6, "h1", "b1", 0), ErrAmount)        // zero
	checkErrCode(t, s.Waive(6, "h1", "b1", -5), ErrInvalidParam) // negative
	checkErrCode(t, s.Waive(6, "h1", "b1", 31), ErrAmount)       // 3 days*10=30 accrued
	if err := s.Waive(6, "h1", "b1", 30); err != nil {           // exact boundary
		t.Fatal(err)
	}
	st, _ := s.Status(6, "h1", "b1")
	if st.WaivedLate != 30 {
		t.Fatalf("waived = %d, want 30", st.WaivedLate)
	}
	// Unpaid principal keeps accruing new late fees after the waiver.
	st, _ = s.Status(8, "h1", "b1")
	if st.AccruedLate != 50 {
		t.Fatalf("accrual should continue after waiver: %+v", st)
	}
	if got := mustTotalDue(t, s, 8, "h1"); got != 120 { // 100 + (50-30)
		t.Fatalf("total due = %d, want 120", got)
	}
	// Waiver does not affect the dunning stage.
	if got := mustStage(t, s, 15, "h1", "b1"); got != 3 {
		t.Fatalf("stage = %d, want 3 (overdue 15)", got)
	}
	// Waiver on a closed bill is a state error.
	if _, err := s.Pay(16, "h1", 140); err != nil { // 20 late + 100 principal + 20 prepay
		t.Fatal(err)
	}
	checkErrCode(t, s.Waive(17, "h1", "b1", 1), ErrInvalidState)
}

// Dunning stages advance exactly at the thresholds and never retreat.
func TestStageThresholdsExact(t *testing.T) {
	s := mustNew(t, testConfig()) // thresholds 5, 10, 15
	mustGenerate(t, s, 0, "h1", "b1", 100, 10)
	cases := []struct {
		now   int
		stage int
	}{
		{10, 0}, {14, 0}, {15, 1}, {19, 1}, {20, 2}, {24, 2}, {25, 3}, {40, 3},
	}
	for _, c := range cases {
		if got := mustStage(t, s, c.now, "h1", "b1"); got != c.stage {
			t.Fatalf("now=%d: stage = %d, want %d", c.now, got, c.stage)
		}
	}
	// Dispute freezes the stage; resolved dispute days are excluded.
	if err := s.Dispute(40, "h1", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveDispute(50, "h1", "b1", 100); err != nil {
		t.Fatal(err)
	}
	// Fresh bill proving dispute days are excluded from the overdue count:
	// due 55, disputed [56,66) -> 10 excluded days.
	mustGenerate(t, s, 55, "h2", "b1", 100, 55)
	if err := s.Dispute(56, "h2", "b1"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveDispute(66, "h2", "b1", 100); err != nil {
		t.Fatal(err)
	}
	if got := mustStage(t, s, 69, "h2", "b1"); got != 0 {
		t.Fatalf("stage = %d, want 0 (overdue 69-55-10=4)", got)
	}
	// Without the exclusion, overdue at 70 would be 15 -> stage 3.
	if got := mustStage(t, s, 70, "h2", "b1"); got != 1 {
		t.Fatalf("stage = %d, want 1 (overdue 70-55-10=5)", got)
	}
}

// Stage-3 bills accept only full settlement; an insufficient payment skips
// them (not an error) and continues to later bills.
func TestStage3FullPaymentOnly(t *testing.T) {
	s := mustNew(t, testConfig())             // thresholds 5,10,15
	mustGenerate(t, s, 0, "h1", "b1", 100, 0) // stage 3 by day 15
	mustGenerate(t, s, 0, "h1", "b2", 30, 30)
	// Day 20: b1 accrued 50 (capped), outstanding 150; b1 is stage 3.
	if got := mustStage(t, s, 20, "h1", "b1"); got != 3 {
		t.Fatalf("b1 stage = %d, want 3", got)
	}
	// 100 < 150: b1 skipped entirely, rest goes to b2, remainder prepaid.
	res, err := s.Pay(20, "h1", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applications) != 1 || res.Applications[0].BillID != "b2" {
		t.Fatalf("b1 should be skipped: %+v", res)
	}
	if res.Prepaid != 70 {
		t.Fatalf("prepaid = %d, want 70", res.Prepaid)
	}
	st, _ := s.Status(20, "h1", "b1")
	if st.PaidLate != 0 || st.PaidPrincipal != 0 {
		t.Fatalf("skipped stage-3 bill must be untouched: %+v", st)
	}
	// Exactly enough settles b1 fully: 50 late + 100 principal.
	res, err = s.Pay(21, "h1", 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applications) != 1 || res.Applications[0].ToLate != 50 || res.Applications[0].ToPrincipal != 100 {
		t.Fatalf("full settlement wrong: %+v", res)
	}
	st, _ = s.Status(21, "h1", "b1")
	if !st.Closed || st.Stage != 3 {
		t.Fatalf("b1 should be closed at stage 3: %+v", st)
	}
}

// Rejections report only the first error in the fixed order:
// invalid param < clock rollback < not found < invalid state < amount.
func TestRejectionOrder(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 5, "h1", "b1", 100, 0)
	if err := s.Dispute(6, "h1", "b1"); err != nil {
		t.Fatal(err)
	}
	// negative amount (param) + past clock + missing household -> param wins.
	_, err := s.Pay(3, "ghost", -1)
	checkErrCode(t, err, ErrInvalidParam)
	// zero amount (amount) + past clock + missing household -> clock wins.
	_, err = s.Pay(3, "ghost", 0)
	checkErrCode(t, err, ErrClockRollback)
	// zero amount (amount) + missing household -> not found wins.
	_, err = s.Pay(7, "ghost", 0)
	checkErrCode(t, err, ErrNotFound)
	// re-ruling (state) + bad bill id -> not found wins.
	checkErrCode(t, s.ResolveDispute(7, "h1", "ghost", 1), ErrNotFound)
	// resolve a non-disputed bill -> invalid state.
	mustGenerate(t, s, 7, "h1", "b2", 10, 7)
	checkErrCode(t, s.ResolveDispute(8, "h1", "b2", 10), ErrInvalidState)
	// zero payment to an existing household -> amount error.
	_, err = s.Pay(8, "h1", 0)
	checkErrCode(t, err, ErrAmount)
	// duplicate bill id -> invalid state.
	checkErrCode(t, s.GenerateBill(8, "h1", "b1", 5, 8), ErrInvalidState)
	// invalid service config.
	bad := testConfig()
	bad.Thresholds = [3]int{5, 5, 9}
	if _, err := NewService(bad); err == nil {
		t.Fatal("expected config error")
	}
}

// A rejected operation changes nothing: bills, payments, prepay and clock.
func TestRejectedOpLeavesNoTrace(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 5, "h1", "b1", 100, 0)
	before, err := s.View(10, "h1")
	if err != nil {
		t.Fatal(err)
	}
	// Rejected: waive exceeds unpaid late fee, at a far-future now.
	checkErrCode(t, s.Waive(100, "h1", "b1", 999), ErrAmount)
	// Rejected: clock rollback.
	checkErrCode(t, s.Dispute(4, "h1", "b1"), ErrClockRollback)
	after, err := s.View(10, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if before.Prepay != after.Prepay || before.TotalPaid != after.TotalPaid {
		t.Fatalf("household totals changed: %+v -> %+v", before, after)
	}
	if len(before.Bills) != len(after.Bills) {
		t.Fatalf("bill set changed")
	}
	for id, bs := range before.Bills {
		if after.Bills[id] != bs {
			t.Fatalf("bill %s changed: %+v -> %+v", id, bs, after.Bills[id])
		}
	}
	// Clock was not advanced by the rejected now=100 op.
	if err := s.Dispute(11, "h1", "b1"); err != nil {
		t.Fatalf("clock should still accept now=11: %v", err)
	}
}

// TotalDue scans only unclosed bills, independent of closed history size.
func TestQueryScansOpenBillsOnly(t *testing.T) {
	s := mustNew(t, testConfig())
	mustGenerate(t, s, 0, "h1", "keep-1", 100, 30)
	mustGenerate(t, s, 0, "h1", "keep-2", 100, 30)
	// Create and close many bills to build up history.
	for i := 0; i < 200; i++ {
		id := "old-" + string(rune('a'+i%26)) + "-" + string(rune('A'+i/26))
		mustGenerate(t, s, 0, "h1", id, 10, 0)
	}
	if _, err := s.Pay(1, "h1", 2000); err != nil { // closes all "old-*" bills
		t.Fatal(err)
	}
	if _, err := s.TotalDue(2, "h1"); err != nil {
		t.Fatal(err)
	}
	if s.scanCount != 2 {
		t.Fatalf("TotalDue scanned %d bills, want 2 (open only)", s.scanCount)
	}
	// Add more closed history; the scan count must not grow.
	for i := 0; i < 300; i++ {
		id := "older-" + string(rune('a'+i%26)) + "-" + string(rune('A'+i/26))
		mustGenerate(t, s, 2, "h1", id, 10, 2)
	}
	if _, err := s.Pay(3, "h1", 3000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TotalDue(4, "h1"); err != nil {
		t.Fatal(err)
	}
	if s.scanCount != 2 {
		t.Fatalf("TotalDue scanned %d bills after more history, want 2", s.scanCount)
	}
}

// Concurrent payments are equivalent to some serial order: conservation
// holds and no bill is ever over-applied.
func TestConcurrentPayments(t *testing.T) {
	s := mustNew(t, testConfig())
	for i := 0; i < 10; i++ {
		mustGenerate(t, s, 0, "h1", "b"+string(rune('a'+i)), 100, 0)
	}
	const payers = 32
	const perPayer = 50
	done := make(chan struct{}, payers)
	for p := 0; p < payers; p++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < perPayer; i++ {
				if _, err := s.Pay(1, "h1", 7); err != nil {
					t.Errorf("pay: %v", err)
					return
				}
			}
		}()
	}
	for p := 0; p < payers; p++ {
		<-done
	}
	v, err := s.View(1, "h1")
	if err != nil {
		t.Fatal(err)
	}
	var appliedP, appliedL int64
	for _, bs := range v.Bills {
		appliedP += bs.PaidPrincipal
		appliedL += bs.PaidLate
		if bs.PaidPrincipal > bs.Principal {
			t.Fatalf("bill over-applied: %+v", bs)
		}
	}
	if v.TotalPaid != payers*perPayer*7 {
		t.Fatalf("total paid = %d, want %d", v.TotalPaid, payers*perPayer*7)
	}
	if v.TotalPaid != appliedP+appliedL+v.Prepay {
		t.Fatalf("conservation violated: paid=%d principal=%d late=%d prepay=%d",
			v.TotalPaid, appliedP, appliedL, v.Prepay)
	}
	if appliedP+appliedL > 10*100 {
		t.Fatalf("applied %d exceeds total billed %d", appliedP+appliedL, 10*100)
	}
}
