package ontology

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{
		BaseTime:             0,
		WeekSeconds:          100,
		ClosingLeadSeconds:   10,
		MaxClosureSeconds:    100,
		MinClosureGapSeconds: 10,
		MaxAdvanceDays:       2,
	}
}

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var callErr *CallError
	if !errors.As(err, &callErr) || callErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func requireOrderStatus(t *testing.T, system *System, id string, status OrderStatus) Order {
	t.Helper()
	order, ok := system.Order(id)
	if !ok || order.Status != status {
		t.Fatalf("order %s = %#v, ok=%v, want status %s", id, order, ok, status)
	}
	return order
}

func TestWrappingIntervalAndExactClosingLead(t *testing.T) {
	system, err := NewSystem(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(-100, WeeklySchedule{{90, 10}}); err != nil {
		t.Fatal(err)
	}
	if err := system.AcceptInstant("ok", 95, 5); err != nil {
		t.Fatalf("exact remaining time accepted: %v", err)
	}
	requireCode(t, system.AcceptInstant("late", 96, 5), ErrNearClosing)
	if err := system.AcceptInstant("next-week", 99, 0); err != nil {
		t.Fatalf("same wrapping interval continues after midnight: %v", err)
	}
}

func TestTouchingIntervalsRejected(t *testing.T) {
	system, err := NewSystem(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.SubmitSchedule(0, WeeklySchedule{{10, 20}, {20, 30}}), ErrInvalidParameter)
	requireCode(t, system.SubmitSchedule(1, WeeklySchedule{{90, 10}, {10, 20}}), ErrInvalidParameter)
}

func TestScheduleBoundaryAndPendingOverwrite(t *testing.T) {
	system, err := NewSystem(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(-100, WeeklySchedule{{0, 50}}); err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(1, WeeklySchedule{{0, 50}}); err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(2, WeeklySchedule{{60, 70}}); err != nil {
		t.Fatal(err)
	}
	if err := system.AcceptInstant("old-before-boundary", 40, 0); err != nil {
		t.Fatalf("old schedule remains effective: %v", err)
	}
	requireCode(t, system.AcceptInstant("old-at-boundary", 100, 0), ErrNotOpen)
	if err := system.AcceptInstant("new-at-boundary", 160, 0); err != nil {
		t.Fatalf("overwritten pending schedule is effective: %v", err)
	}
}

func TestReservationUsesPendingWeekAndAdvanceEquality(t *testing.T) {
	cfg := testConfig()
	cfg.ClosingLeadSeconds = 0
	system, err := NewSystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(-1, WeeklySchedule{{0, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(1, WeeklySchedule{{30, 40}}); err != nil {
		t.Fatal(err)
	}
	if err := system.AcceptReservation("future", 5, 130, 0); err != nil {
		t.Fatalf("reservation in pending-schedule week: %v", err)
	}
	requireCode(t, system.AcceptReservation("closed", 5, 105, 0), ErrNotOpen)
	advanceSystem, err := NewSystem(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := advanceSystem.SubmitSchedule(-1, WeeklySchedule{{0, 0}}); err != nil {
		t.Fatal(err)
	}
	maxPickup := int64(5 + 2*daySeconds)
	if err := advanceSystem.AcceptReservation("exact-advance", 5, maxPickup, 0); err != nil {
		t.Fatalf("maximum lead equality accepted: %v", err)
	}
	requireCode(t, advanceSystem.AcceptReservation("too-far", 5, maxPickup+1, 0), ErrReservationFar)
}

func TestTemporaryClosureGapAndEarlyEnd(t *testing.T) {
	cfg := testConfig()
	cfg.MinClosureGapSeconds = 10
	system, err := NewSystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := system.StartTemporaryClosure("first", 0, 0, 10, false); err != nil {
		t.Fatal(err)
	}
	if err := system.EndTemporaryClosure("first", 5); err != nil {
		t.Fatal(err)
	}
	if err := system.StartTemporaryClosure("exact-gap", 15, 15, 5, false); err != nil {
		t.Fatalf("gap measured from actual end: %v", err)
	}
	if err := system.EndTemporaryClosure("exact-gap", 17); err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.StartTemporaryClosure("too-soon", 20, 20, 1, false), ErrClosureGap)
}

func TestOpenInstantOrdersBlockOrCancelClosure(t *testing.T) {
	cfg := testConfig()
	cfg.ClosingLeadSeconds = 0
	cfg.MinClosureGapSeconds = 0
	system, err := NewSystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(-1, WeeklySchedule{{0, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := system.AcceptInstant("open", 0, 10); err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.StartTemporaryClosure("blocked", 5, 20, 5, false), ErrOpenOrders)
	if err := system.StartTemporaryClosure("cancel", 6, 20, 5, true); err != nil {
		t.Fatal(err)
	}
	order := requireOrderStatus(t, system, "open", OrderCanceled)
	if order.Responsibility != ResponsibilityMerchant {
		t.Fatalf("responsibility = %s, want merchant", order.Responsibility)
	}
}

func TestForceClosePriorityAndOrderFates(t *testing.T) {
	cfg := testConfig()
	cfg.ClosingLeadSeconds = 0
	cfg.MinClosureGapSeconds = 0
	system, err := NewSystem(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := system.SubmitSchedule(-1, WeeklySchedule{{0, 0}}); err != nil {
		t.Fatal(err)
	}
	if err := system.AcceptInstant("started", 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := system.AcceptInstant("waiting", 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := system.StartOrder("started", 2); err != nil {
		t.Fatal(err)
	}
	if err := system.ForceClose(10); err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.StartTemporaryClosure("during-force", 11, 11, 1, false), ErrForcedClosure)
	requireCode(t, system.AcceptInstant("during-force-order", 12, 0), ErrForcedClosure)
	requireOrderStatus(t, system, "started", OrderStarted)
	canceled := requireOrderStatus(t, system, "waiting", OrderCanceled)
	if canceled.Responsibility != ResponsibilityPlatform {
		t.Fatalf("responsibility = %s, want platform", canceled.Responsibility)
	}
	if err := system.LiftForceClose(20); err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.StartOrder("waiting", 21), ErrInvalidState)
	if err := system.CompleteOrder("started", 22); err != nil {
		t.Fatal(err)
	}
}

func TestClockRollbackDoesNotMutateState(t *testing.T) {
	system, err := NewSystem(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := system.ForceClose(10); err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.LiftForceClose(9), ErrClockRollback)
	requireCode(t, system.ForceClose(10), ErrInvalidState)
	if err := system.LiftForceClose(11); err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.LiftForceClose(12), ErrInvalidState)
}
