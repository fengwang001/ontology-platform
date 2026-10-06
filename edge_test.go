package ontology

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{
		AdvanceMin:      10,
		MaxAdvanceMin:   100,
		CheckInLeadMin:  5,
		MaxWorkDays:     3,
		ComplaintLimit:  2,
		ReapplyWaitDays: 2,
		RefundDays:      2,
		Penalty:         100,
	}
}

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil", code)
	}
	var serviceError Error
	if !errors.As(err, &serviceError) || serviceError.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func setupService(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.RegisterHousehold(0, "h1"))
	must(t, service.RegisterHousehold(0, "h2"))
	must(t, service.RegisterHousehold(0, "h3"))
	must(t, service.RegisterHousehold(0, "h4"))
	must(t, service.RegisterElevator(0, "e1"))
	must(t, service.TopUp(0, "h1", 200))
	must(t, service.TopUp(0, "h2", 200))
	must(t, service.TopUp(0, "h3", 200))
	must(t, service.TopUp(0, "h4", 200))
	return service
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestBookingLeadAndCancelBoundary(t *testing.T) {
	service := setupService(t)
	if _, err := service.BookMove(0, "h1", "e1", 9, 10, MoveIn); err == nil {
		t.Fatal("lead below L accepted")
	}
	first, err := service.BookMove(0, "h1", "e1", 10, 11, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.CancelBooking(0, first))
	if _, err := service.BookMove(1, "h2", "e1", 101, 102, MoveOut); err != nil {
		t.Fatal(err)
	}
	balance, _ := service.Balance(1, "h1")
	if balance != 200 {
		t.Fatalf("cancel at exactly L changed balance to %d", balance)
	}

	late, err := service.BookMove(20, "h1", "e1", 30, 31, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.CancelBooking(21, late))
	balance, _ = service.Balance(21, "h1")
	if balance != 100 {
		t.Fatalf("late cancel balance = %d", balance)
	}
}

func TestCheckInWindowAndOverrun(t *testing.T) {
	service := setupService(t)
	first, err := service.BookMove(0, "h1", "e1", 20, 30, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.BookMove(0, "h2", "e1", 30, 40, MoveOut)
	if err != nil {
		t.Fatal(err)
	}
	third, err := service.BookMove(0, "h3", "e1", 50, 60, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	lateShow, err := service.BookMove(10, "h4", "e1", 90, 100, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, service.BookingCheckIn(14, first), ErrTimeWindow)
	must(t, service.BookingCheckIn(15, first))
	if err := service.BookingCheckIn(25, second); err == nil {
		t.Fatal("later household checked in before checkout")
	}
	must(t, service.BookingCheckOut(45, first))
	requireCode(t, service.BookingCheckIn(45, second), ErrTimeWindow)
	must(t, service.BookingCheckIn(45, third))
	must(t, service.BookingCheckOut(50, third))
	record, _ := service.GetBooking(50, first)
	if record.Status != BookingDone {
		t.Fatalf("first status = %s", record.Status)
	}

	noShow, err := service.BookMove(50, "h1", "e1", 70, 80, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.BookingCheckIn(80, noShow))
	must(t, service.BookingCheckOut(81, noShow))

	record, _ = service.GetBooking(101, lateShow)
	if record.Status != BookingNoShow {
		t.Fatalf("status at expiry = %s", record.Status)
	}
}

func TestPermitDurationExtensionQuietHolidayAndComplaints(t *testing.T) {
	service := setupService(t)
	permitID, err := service.ApplyPermit(0, "h1", 1, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.ReviewPermit(10, permitID, true))
	requireCode(t, service.ExtendPermit(100, permitID, 2), ErrIllegalArgument)
	requireCode(t, service.ExtendPermit(101, permitID, 5), ErrIllegalArgument)
	holidayPermit, err := service.ApplyPermit(102, "h2", 2, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.ReviewPermit(110, holidayPermit, true))
	must(t, service.ExtendPermit(MinutesPerDay+10, permitID, 4))
	requireCode(t, service.ExtendPermit(MinutesPerDay+11, permitID, 5), ErrTimeWindow)
	must(t, service.AddHoliday(MinutesPerDay+20, 2))
	requireCode(t, service.PermitCheckIn(2*MinutesPerDay, holidayPermit), ErrQuietConflict)

	must(t, service.SetQuietRanges(3*MinutesPerDay+20, []MinuteRange{{Start: 23 * 60, End: 60}}))
	requireCode(t, service.PermitCheckIn(3*MinutesPerDay+30, permitID), ErrQuietConflict)
	must(t, service.PermitCheckIn(3*MinutesPerDay+120, permitID))
	must(t, service.PermitCheckOut(3*MinutesPerDay+140, permitID))

	must(t, service.AddComplaint(3*MinutesPerDay+220, permitID))
	requireCode(t, service.PermitCheckIn(3*MinutesPerDay+230, permitID), ErrInvalidState)
	must(t, service.LiftSuspension(3*MinutesPerDay+230, permitID))
	must(t, service.AddComplaint(3*MinutesPerDay+240, permitID))
	record, _ := service.GetPermit(3*MinutesPerDay+240, permitID)
	if record.Status != PermitRevoked {
		t.Fatalf("status after K complaints = %s", record.Status)
	}
	if _, err := service.ApplyPermit(3*MinutesPerDay+400, "h1", 4, 5, false); err == nil {
		t.Fatal("reapplication before W accepted")
	} else {
		requireCode(t, err, ErrTimeWindow)
	}
	next, err := service.ApplyPermit(5*MinutesPerDay, "h1", 6, 7, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = next
}

func TestRefundRestartDepositBoundaryAndRejectionOrder(t *testing.T) {
	service := setupService(t)
	permitID, err := service.ApplyPermit(0, "h1", 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	must(t, service.ReviewPermit(10, permitID, true))
	must(t, service.PermitCheckIn(MinutesPerDay, permitID))
	must(t, service.PermitCheckOut(MinutesPerDay+10, permitID))
	requireCode(t, service.RefundPermit(3*MinutesPerDay, permitID), ErrTimeWindow)
	must(t, service.AddComplaint(4*MinutesPerDay, permitID))
	requireCode(t, service.RefundPermit(5*MinutesPerDay, permitID), ErrTimeWindow)
	must(t, service.RefundPermit(6*MinutesPerDay, permitID))

	bookingID, err := service.BookMove(6*MinutesPerDay+1, "h1", "e1", 6*MinutesPerDay+20, 6*MinutesPerDay+21, MoveIn)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := service.GetBooking(6*MinutesPerDay+22, bookingID)
	if record.Status != BookingNoShow {
		t.Fatalf("status = %s", record.Status)
	}
	balance, _ := service.Balance(6*MinutesPerDay+22, "h1")
	if balance != 0 {
		t.Fatalf("balance after exact one remaining penalty = %d", balance)
	}
	if _, err := service.BookMove(6*MinutesPerDay+30, "h1", "e1", 6*MinutesPerDay+50, 6*MinutesPerDay+51, MoveIn); err == nil {
		t.Fatal("booking accepted without deposit")
	}

	calledClock, err := service.Balance(-1, "missing")
	if err == nil || calledClock != 0 {
		t.Fatalf("illegal parameter did not report first: %v", err)
	}
	clockBefore := service.clock
	_, err = service.BookMove(6*MinutesPerDay, "h1", "e1", 6*MinutesPerDay+50, 6*MinutesPerDay+51, MoveIn)
	requireCode(t, err, ErrClockRewind)
	if service.clock != clockBefore {
		t.Fatalf("rejected clock changed from %d to %d", clockBefore, service.clock)
	}
	_, err = service.Balance(6*MinutesPerDay+31, "missing")
	requireCode(t, err, ErrNotFound)
}

func TestUnapprovedPermitExpiryReleasesHold(t *testing.T) {
	service := setupService(t)
	first, err := service.ApplyPermit(0, "h1", 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	record, err := service.GetPermit(2*MinutesPerDay, first)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != PermitExpired {
		t.Fatalf("unapproved permit status = %s", record.Status)
	}
	if _, err := service.ApplyPermit(2*MinutesPerDay, "h1", 3, 4, false); err != nil {
		t.Fatalf("new application after unapproved expiry rejected: %v", err)
	}
}
