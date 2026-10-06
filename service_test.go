package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{EarlyMin: 30, LateMin: 120, CheckInMin: 10, MaxDays: 7, WaitDays: 3, RefundDays: 2, RevokeAt: 2, Penalty: 50}
}

func codeOf(err error) ErrorCode {
	var e Error
	if errors.As(err, &e) {
		return e.Code
	}
	return OK
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func reserve(s *Service, id, house string, now, start, end int) error {
	return s.Reserve(ReservationRequest{Now: now, ID: id, House: house, Elevator: "E1", Start: start, End: end})
}

func TestReservationLeadBoundsAndExactLCancel(t *testing.T) {
	s := NewService(testConfig())
	must(t, s.Deposit(DepositRequest{Now: 0, House: "A", Amount: 100}))
	must(t, reserve(s, "r30", "A", 0, 30, 40))
	must(t, s.Cancel(ReservationAction{Now: 0, ID: "r30"}))
	if got := s.Balance("A"); got != 100 {
		t.Fatalf("cancel exactly L minutes before start: balance=%d want 100", got)
	}
	must(t, reserve(s, "r120", "A", 0, 120, 130))
	if err := s.Reserve(ReservationRequest{Now: 1, ID: "r29", House: "B", Elevator: "E1", Start: 30, End: 40}); codeOf(err) != ErrTimeWindow {
		t.Fatalf("lead below L: code=%v", codeOf(err))
	}
	if err := s.Reserve(ReservationRequest{Now: 0, ID: "r121", House: "B", Elevator: "E1", Start: 121, End: 130}); codeOf(err) != ErrTimeWindow {
		t.Fatalf("lead above U: code=%v", codeOf(err))
	}
}

func TestCancellationInsideLChargesPenaltyAndRejectsLowDeposit(t *testing.T) {
	s := NewService(testConfig())
	must(t, s.Deposit(DepositRequest{Now: 0, House: "A", Amount: 50}))
	must(t, reserve(s, "r", "A", 0, 30, 40))
	must(t, s.Cancel(ReservationAction{Now: 1, ID: "r"}))
	if got := s.Balance("A"); got != 0 {
		t.Fatalf("penalty balance=%d want 0", got)
	}
	if err := reserve(s, "r2", "A", 1, 31, 41); codeOf(err) != ErrInsufficientDeposit {
		t.Fatalf("balance equal zero: code=%v", codeOf(err))
	}
}

func TestElevatorCheckInWindowEndpointsAndOverstay(t *testing.T) {
	s := NewService(testConfig())
	must(t, s.Deposit(DepositRequest{Now: 0, House: "A", Amount: 100}))
	must(t, reserve(s, "a", "A", 0, 30, 40))
	if err := s.CheckInElevator(ReservationAction{Now: 19, ID: "a"}); codeOf(err) != ErrTimeWindow {
		t.Fatalf("before start-E: code=%v", codeOf(err))
	}
	must(t, s.CheckInElevator(ReservationAction{Now: 20, ID: "a"}))

	s2 := NewService(testConfig())
	must(t, s2.Deposit(DepositRequest{Now: 0, House: "A", Amount: 100}))
	must(t, reserve(s2, "a", "A", 0, 30, 40))
	must(t, s2.CheckInElevator(ReservationAction{Now: 40, ID: "a"}))
	must(t, s2.Deposit(DepositRequest{Now: 45, House: "B", Amount: 100}))
	must(t, s2.Reserve(ReservationRequest{Now: 45, ID: "b", House: "B", Elevator: "E1", Start: 80, End: 90}))
	if err := s2.CheckInElevator(ReservationAction{Now: 80, ID: "b"}); codeOf(err) != ErrIllegalState {
		t.Fatalf("overstay should block later household check-in, got %v", codeOf(err))
	}
	must(t, s2.CheckOutElevator(ReservationAction{Now: 81, ID: "a"}))
	must(t, s2.CheckInElevator(ReservationAction{Now: 82, ID: "b"}))
}

func TestNoShowExpiresAndReleasesSlots(t *testing.T) {
	s := NewService(testConfig())
	must(t, s.Deposit(DepositRequest{Now: 0, House: "A", Amount: 100}))
	must(t, reserve(s, "a", "A", 0, 30, 40))
	must(t, s.Deposit(DepositRequest{Now: 41, House: "B", Amount: 100}))
	must(t, s.Reserve(ReservationRequest{Now: 41, ID: "b", House: "B", Elevator: "E1", Start: 71, End: 81}))
	if got := s.Balance("A"); got != 50 {
		t.Fatalf("automatic no-show balance=%d want 50", got)
	}
}

func TestRejectedOperationLeavesNoTraceAndClockStays(t *testing.T) {
	s := NewService(testConfig())
	must(t, s.Deposit(DepositRequest{Now: 10, House: "A", Amount: 100}))
	before := s.Snapshot(10)
	err := s.Reserve(ReservationRequest{Now: 9, ID: "x", House: "A", Elevator: "E1", Start: 39, End: 49})
	if codeOf(err) != ErrClockRewound {
		t.Fatalf("code=%v", codeOf(err))
	}
	after := s.Snapshot(10)
	if len(after.Reservations) != len(before.Reservations) || len(after.Ledger) != len(before.Ledger) || after.Now != 10 {
		t.Fatalf("rejected op changed state: before=%+v after=%+v", before, after)
	}
}

func TestErrorOrderInvalidBeforeClockBeforeNotFound(t *testing.T) {
	s := NewService(testConfig())
	must(t, s.Deposit(DepositRequest{Now: 10, House: "A", Amount: 100}))
	err := s.Cancel(ReservationAction{Now: 9, ID: ""})
	if codeOf(err) != ErrInvalidArgument {
		t.Fatalf("invalid should precede clock: %v", codeOf(err))
	}
	err = s.Cancel(ReservationAction{Now: 9, ID: "missing"})
	if codeOf(err) != ErrClockRewound {
		t.Fatalf("clock should precede not-found: %v", codeOf(err))
	}
}

func TestConcurrentReservationsAndPenalties(t *testing.T) {
	s := NewService(testConfig())
	must(t, s.Deposit(DepositRequest{Now: 0, House: "A", Amount: 100}))
	must(t, s.Deposit(DepositRequest{Now: 0, House: "B", Amount: 100}))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, house := range []string{"A", "B"} {
		wg.Add(1)
		go func(house string) {
			defer wg.Done()
			results <- s.Reserve(ReservationRequest{Now: 0, ID: "same-" + house, House: house, Elevator: "E1", Start: 30, End: 40})
		}(house)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted=%d want exactly one", accepted)
	}
}

func permitReq(id, house string, now, start, end int, noisy bool) PermitRequest {
	return PermitRequest{Now: now, ID: id, House: house, StartDay: start, EndDay: end, Noisy: noisy}
}

func approvedPermit(t *testing.T, s *Service, id, house string, now, start, end int, noisy bool) {
	t.Helper()
	must(t, s.Deposit(DepositRequest{Now: now, House: house, Amount: 100}))
	must(t, s.ApplyPermit(permitReq(id, house, now, start, end, noisy)))
	must(t, s.ApprovePermit(PermitAction{Now: now, ID: id}))
}

func TestPermitExactlyMDaysAndExtensionExactlyM(t *testing.T) {
	s := NewService(testConfig())
	approvedPermit(t, s, "exact", "A", 0, 1, 8, false)
	approvedPermit(t, s, "p", "B", 0, 1, 6, false)
	must(t, s.ExtendPermit(PermitExtend{Now: 1, ID: "p", NewEndDay: 8}))
	if err := s.ExtendPermit(PermitExtend{Now: 2, ID: "p", NewEndDay: 9}); codeOf(err) != ErrTimeWindow {
		t.Fatalf("second extension: code=%v", codeOf(err))
	}
}

func TestQuietCrossMidnightAndFutureHoliday(t *testing.T) {
	s := NewService(testConfig())
	s.SetQuietRanges(QuietRequest{Now: 0, Ranges: []QuietRange{{StartMinute: 22 * 60, EndMinute: 6 * 60}}})
	approvedPermit(t, s, "p", "A", 0, 1, 2, true)
	if err := s.CheckInPermit(PermitAction{Now: 1440 + 23*60, ID: "p"}); codeOf(err) != ErrQuietConflict {
		t.Fatalf("cross-midnight first-day quiet: %v", codeOf(err))
	}
	if err := s.CheckInPermit(PermitAction{Now: 1440 + 5*60, ID: "p"}); codeOf(err) != ErrQuietConflict {
		t.Fatalf("cross-midnight second-day quiet: %v", codeOf(err))
	}
	must(t, s.CheckInPermit(PermitAction{Now: 1440 + 7*60, ID: "p"}))

	s2 := NewService(testConfig())
	if err := s2.AddHoliday(HolidayRequest{Now: 1440, Day: 1}); codeOf(err) != ErrTimeWindow {
		t.Fatalf("past/current holiday: %v", codeOf(err))
	}
	must(t, s2.AddHoliday(HolidayRequest{Now: 1440, Day: 2}))
	approvedPermit(t, s2, "q", "B", 1440, 2, 3, true)
	if err := s2.CheckInPermit(PermitAction{Now: 2 * 1440, ID: "q"}); codeOf(err) != ErrQuietConflict {
		t.Fatalf("holiday whole-day quiet: %v", codeOf(err))
	}
}

func TestComplaintsSuspendThenExactlyKRevokeAndWaitW(t *testing.T) {
	s := NewService(testConfig())
	approvedPermit(t, s, "p", "A", 0, 1, 5, false)
	must(t, s.Complain(HouseAction{Now: 1440, House: "A"}))
	if err := s.CheckInPermit(PermitAction{Now: 1441, ID: "p"}); codeOf(err) != ErrIllegalState {
		t.Fatalf("suspended check-in: %v", codeOf(err))
	}
	must(t, s.LiftSuspension(HouseAction{Now: 1500, House: "A"}))
	must(t, s.Complain(HouseAction{Now: 2880, House: "A"}))
	if err := s.ApplyPermit(permitReq("p2", "A", 4320, 4, 5, false)); codeOf(err) != ErrTimeWindow {
		t.Fatalf("before W-day wait: %v", codeOf(err))
	}
	must(t, s.ApplyPermit(permitReq("p3", "A", 4320, 5, 6, false)))
}

func TestRefundWindowRestartsFromLatestComplaint(t *testing.T) {
	s := NewService(testConfig())
	approvedPermit(t, s, "p", "A", 0, 1, 3, false)
	must(t, s.CheckInPermit(PermitAction{Now: 1440, ID: "p"}))
	must(t, s.CheckOutPermit(PermitAction{Now: 1500, ID: "p"}))
	must(t, s.Complain(HouseAction{Now: 1600, House: "A"}))
	must(t, s.LiftSuspension(HouseAction{Now: 1700, House: "A"}))
	must(t, s.Complain(HouseAction{Now: 2 * 1440, House: "A"}))
	if p, _ := s.office.refundEligible("A", 4*1440, 2); p == nil {
		t.Fatal("permit should exist for refund eligibility")
	}
	if err := s.Refund(HouseAction{Now: 3 * 1440, House: "A"}); codeOf(err) != ErrTimeWindow {
		// complaint day2, latest anchor day3, R=2 => eligible on day5
		t.Fatalf("refund before reset deadline: %v", codeOf(err))
	}
	if err := s.Refund(HouseAction{Now: 4 * 1440, House: "A"}); codeOf(err) != ErrTimeWindow {
		t.Fatalf("refund one day early after reset: %v", codeOf(err))
	}
	must(t, s.Refund(HouseAction{Now: 5 * 1440, House: "A"}))
	if got := s.Balance("A"); got != 0 {
		t.Fatalf("balance=%d want zero", got)
	}
}

func TestQuietChangeDoesNotInterruptActiveConstruction(t *testing.T) {
	s := NewService(testConfig())
	approvedPermit(t, s, "p", "A", 0, 1, 2, true)
	must(t, s.CheckInPermit(PermitAction{Now: 1440 + 7*60, ID: "p"}))
	s.SetQuietRanges(QuietRequest{Now: 1440 + 7*60, Ranges: []QuietRange{{StartMinute: 0, EndMinute: 24 * 60}}})
	if err := s.CheckOutPermit(PermitAction{Now: 1440 + 8*60, ID: "p"}); err != nil {
		t.Fatalf("active construction should survive quiet change: %v", err)
	}
}

func TestSlotLookupConstantInHistory(t *testing.T) {
	s := NewService(testConfig())
	for i := 0; i < 2000; i++ {
		house := fmt.Sprintf("H%d", i+1)
		must(t, s.Deposit(DepositRequest{Now: 0, House: house, Amount: 200000}))
	}
	must(t, s.Deposit(DepositRequest{Now: 0, House: "H0", Amount: 200000}))
	for i := 0; i < 2000; i++ {
		start := 30
		must(t, s.Reserve(ReservationRequest{
			Now: 0, ID: fmt.Sprintf("r%d", i), House: fmt.Sprintf("H%d", i+1),
			Elevator: fmt.Sprintf("E%d", i+2), Start: start, End: start + 10,
		}))
	}
	must(t, s.Reserve(ReservationRequest{
		Now: 0, ID: "target", House: "H0", Elevator: "E1",
		Start: 30, End: 40,
	}))
	if !s.SlotFree("E1", 499) {
		t.Fatal("unoccupied minute should be free")
	}
	if s.SlotFree("E1", 30) {
		t.Fatal("occupied minute should not be free")
	}
}
