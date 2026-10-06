package settlement

import (
	"errors"
	"io"
	"log"
	"testing"
)

type testLogger struct {
	t *testing.T
}

func (logger testLogger) Printf(format string, args ...any) {
	logger.t.Logf(format, args...)
}

func newTestSystem(t *testing.T, config Config, accounts []Account) *System {
	t.Helper()
	system, err := New(config, accounts)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	system.SetLogger(testLogger{t: t})
	return system
}

func discardLogger() Logger {
	return log.New(io.Discard, "", 0)
}

func TestSettlementDueTodayAndPartialAndAllOrNone(t *testing.T) {
	system := newTestSystem(t, Config{BusinessDays: []int{1, 2}, MaxFailDays: 3, PenaltyBPS: 100}, []Account{
		{ID: "buyer", Cash: 100},
		{ID: "seller", Cash: 0, Holdings: map[SecurityID]int64{"X": 7}},
	})

	partial := OrderInput{ID: 1, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 10, Price: 10, SettlementDay: 1, AllowPartial: true}
	fullOnly := OrderInput{ID: 2, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 10, Price: 10, SettlementDay: 1}
	if err := system.RegisterOrder(partial); err != nil {
		t.Fatalf("RegisterOrder partial: %v", err)
	}
	if err := system.RegisterOrder(fullOnly); err != nil {
		t.Fatalf("RegisterOrder full-only: %v", err)
	}

	result, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 10})
	if err != nil {
		t.Fatalf("ProcessBusinessDay: %v", err)
	}
	if result.Processed[0].Delivered != 7 || result.Processed[1].Delivered != 0 {
		t.Fatalf("fills = %+v, want 7 and 0", result.Processed)
	}
	if got := orderStatus(t, system, 1); got != StatusPartial {
		t.Fatalf("order 1 status = %s", got)
	}
	if got := orderStatus(t, system, 2); got != StatusPending {
		t.Fatalf("order 2 status = %s", got)
	}
	assertPosition(t, system, "buyer", 30, map[SecurityID]int64{"X": 7})
	assertPosition(t, system, "seller", 70, map[SecurityID]int64{"X": 0})
}

func TestResponsibilitySellerWhenBothSidesShort(t *testing.T) {
	system := newTestSystem(t, Config{BusinessDays: []int{1}, MaxFailDays: 3, PenaltyBPS: 1}, []Account{
		{ID: "buyer", Cash: 1},
		{ID: "seller"},
	})
	mustRegister(t, system, OrderInput{ID: 1, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 10, Price: 10, SettlementDay: 1})

	result, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 10})
	if err != nil {
		t.Fatalf("ProcessBusinessDay: %v", err)
	}
	if result.Processed[0].ResponsibleIfFailed != ResponsibleSeller {
		t.Fatalf("responsible = %s", result.Processed[0].ResponsibleIfFailed)
	}
	if result.Penalties[0].Responsible != "seller" || result.Penalties[0].Amount != 1 {
		t.Fatalf("penalty = %+v", result.Penalties[0])
	}
}

func TestSecuritiesReceivedAreUnavailableUntilNextDay(t *testing.T) {
	system := newTestSystem(t, Config{BusinessDays: []int{1, 2}, MaxFailDays: 3, PenaltyBPS: 1}, []Account{
		{ID: "a", Cash: 100},
		{ID: "b", Cash: 100, Holdings: map[SecurityID]int64{"X": 5}},
		{ID: "c", Cash: 100},
	})
	mustRegister(t, system, OrderInput{ID: 1, Security: "X", Buyer: "a", Seller: "b", Quantity: 5, Price: 10, SettlementDay: 1, AllowPartial: true})
	mustRegister(t, system, OrderInput{ID: 2, Security: "X", Buyer: "c", Seller: "a", Quantity: 5, Price: 10, SettlementDay: 1, AllowPartial: true})

	result, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 10})
	if err != nil {
		t.Fatalf("ProcessBusinessDay: %v", err)
	}
	if result.Processed[0].Delivered != 5 || result.Processed[1].Delivered != 0 {
		t.Fatalf("same-batch resale fills = %+v", result.Processed)
	}

	result, err = system.ProcessBusinessDay(2, map[SecurityID]int64{"X": 10})
	if err != nil {
		t.Fatalf("ProcessBusinessDay day 2: %v", err)
	}
	if result.Processed[0].Delivered != 5 {
		t.Fatalf("next-day fill = %+v", result.Processed[0])
	}
}

func TestForceCloseAtExactlyBAndNegativeCompensationZero(t *testing.T) {
	system := newTestSystem(t, Config{BusinessDays: []int{1, 2}, MaxFailDays: 2, PenaltyBPS: 1}, []Account{
		{ID: "buyer", Cash: 100},
		{ID: "seller"},
	})
	mustRegister(t, system, OrderInput{ID: 1, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 2, Price: 10, SettlementDay: 1})

	first, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 4})
	if err != nil || len(first.ForceClosures) != 0 {
		t.Fatalf("first day = %+v, %v", first, err)
	}
	second, err := system.ProcessBusinessDay(2, map[SecurityID]int64{"X": 4})
	if err != nil {
		t.Fatalf("second day: %v", err)
	}
	if len(second.ForceClosures) != 1 || second.ForceClosures[0].CompensationAmount != 0 {
		t.Fatalf("force closures = %+v", second.ForceClosures)
	}
	if got := orderStatus(t, system, 1); got != StatusForced {
		t.Fatalf("status = %s", got)
	}
	comp, err := system.CompensationBalances("seller")
	if err != nil || comp.Payable != 0 {
		t.Fatalf("seller compensation = %+v, %v", comp, err)
	}
}

func TestForceCloseResponsibilityAndCompensation(t *testing.T) {
	t.Run("buyer forced close has no compensation", func(t *testing.T) {
		system := newTestSystem(t, Config{BusinessDays: []int{1}, MaxFailDays: 1, PenaltyBPS: 1}, []Account{
			{ID: "buyer"},
			{ID: "seller", Holdings: map[SecurityID]int64{"X": 2}},
		})
		mustRegister(t, system, OrderInput{ID: 1, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 2, Price: 10, SettlementDay: 1, AllowPartial: true})

		result, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 20})
		if err != nil {
			t.Fatalf("ProcessBusinessDay: %v", err)
		}
		if result.Penalties[0].Responsible != "buyer" {
			t.Fatalf("responsible = %s", result.Penalties[0].Responsible)
		}
		if len(result.ForceClosures) != 1 || result.ForceClosures[0].CompensationAmount != 0 || result.ForceClosures[0].Payer != "" {
			t.Fatalf("force closures = %+v", result.ForceClosures)
		}
	})

	t.Run("seller pays positive reference price difference", func(t *testing.T) {
		system := newTestSystem(t, Config{BusinessDays: []int{1}, MaxFailDays: 1, PenaltyBPS: 1}, []Account{
			{ID: "buyer", Cash: 100},
			{ID: "seller"},
		})
		mustRegister(t, system, OrderInput{ID: 1, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 2, Price: 10, SettlementDay: 1, AllowPartial: true})

		result, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 15})
		if err != nil {
			t.Fatalf("ProcessBusinessDay: %v", err)
		}
		if result.ForceClosures[0].CompensationAmount != 10 {
			t.Fatalf("compensation = %d, want 10", result.ForceClosures[0].CompensationAmount)
		}
		comp, err := system.CompensationBalances("seller")
		if err != nil || comp.Payable != 10 {
			t.Fatalf("seller compensation = %+v, %v", comp, err)
		}
	})
}

func TestErrorPriorityAndRejectedOperationsLeaveNoTrace(t *testing.T) {
	system := newTestSystem(t, Config{BusinessDays: []int{1, 2}, MaxFailDays: 2, PenaltyBPS: 1}, []Account{
		{ID: "buyer"},
		{ID: "seller"},
	})

	if _, err := system.ProcessBusinessDay(3, map[SecurityID]int64{"X": 1}); !errors.Is(err, ErrNonBusinessDay) {
		t.Fatalf("non-business day error = %v", err)
	}
	if _, err := system.ProcessBusinessDay(3, nil); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("non-business invalid parameter priority = %v", err)
	}
	mustRegister(t, system, OrderInput{ID: 1, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 1, Price: 1, SettlementDay: 1})
	if _, err := system.ProcessBusinessDay(1, nil); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("nil prices error = %v", err)
	}
	if _, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"Y": 1}); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("missing price error = %v", err)
	}
	if _, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 1}); err != nil {
		t.Fatalf("valid retry after rejected calls: %v", err)
	}
	if _, err := system.ProcessBusinessDay(1, nil); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("duplicate day with invalid prices error = %v", err)
	}
	if err := system.RegisterOrder(OrderInput{ID: 1, Security: "X", Buyer: "missing", Seller: "seller", Quantity: 1, Price: 1, SettlementDay: 1}); !errors.Is(err, ErrDuplicateOrder) {
		t.Fatalf("duplicate has priority over missing account: %v", err)
	}
	if _, err := system.ProcessBusinessDay(2, map[SecurityID]int64{"X": 1}); err != nil {
		t.Fatalf("process day 2: %v", err)
	}
	if err := system.RegisterOrder(OrderInput{ID: 2, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 1, Price: 1, SettlementDay: 1}); !errors.Is(err, ErrDatePassed) {
		t.Fatalf("date passed error = %v", err)
	}
	if _, err := system.Order(2); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("rejected order visible: %v", err)
	}
}

func mustRegister(t *testing.T, system *System, input OrderInput) {
	t.Helper()
	if err := system.RegisterOrder(input); err != nil {
		t.Fatalf("RegisterOrder %+v: %v", input, err)
	}
}

func orderStatus(t *testing.T, system *System, id uint64) OrderStatus {
	t.Helper()
	view, err := system.Order(id)
	if err != nil {
		t.Fatalf("Order %d: %v", id, err)
	}
	return view.Status
}

func assertPosition(t *testing.T, system *System, accountID AccountID, cash int64, holdings map[SecurityID]int64) {
	t.Helper()
	position, err := system.Position(accountID)
	if err != nil {
		t.Fatalf("Position %s: %v", accountID, err)
	}
	if position.Cash != cash {
		t.Fatalf("%s cash = %d, want %d", accountID, position.Cash, cash)
	}
	for security, quantity := range holdings {
		if position.Holdings[security] != quantity {
			t.Fatalf("%s %s = %d, want %d", accountID, security, position.Holdings[security], quantity)
		}
	}
}
