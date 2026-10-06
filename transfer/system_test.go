package transfer

import (
	"errors"
	"testing"
)

func requireFailure(t *testing.T, err error, kind ErrorKind) Failure {
	t.Helper()
	var failure Failure
	if !errors.As(err, &failure) || failure.Kind != kind {
		t.Fatalf("got %v, want kind %s", err, kind)
	}
	return failure
}

func TestCreateIsAtomicAndReportsSmallestInsufficientLine(t *testing.T) {
	system := NewSystem(map[Warehouse]map[Product]Quantity{
		"src": {"p1": 10, "p2": 3, "p3": 1},
		"dst": {},
	}, Config{TolerancePerMille: 100, WaitDuration: 10})

	err := system.CreateOrder("o1", "src", "dst", []Line{
		{Product: "p1", Quantity: 5},
		{Product: "p2", Quantity: 4},
		{Product: "p3", Quantity: 2},
	}, 1)
	failure := requireFailure(t, err, KindInsufficientStock)
	if failure.LineIndex != 1 {
		t.Fatalf("insufficient line = %d, want 1", failure.LineIndex)
	}

	assertStock(t, system, "src", "p1", 10, 0)
	assertStock(t, system, "src", "p2", 3, 0)
	assertStock(t, system, "src", "p3", 1, 0)
	if !system.VerifyConservation() {
		t.Fatal("conservation failed after rejected batch create")
	}
}

func TestCancelReleasesCreatedFreezeOnly(t *testing.T) {
	system := newTwoWarehouseSystem(Config{TolerancePerMille: 100, WaitDuration: 10})
	must(t, system.CreateOrder("o1", "src", "dst", []Line{{Product: "p1", Quantity: 4}}, 1))
	assertStock(t, system, "src", "p1", 6, 4)

	must(t, system.CancelOrder("o1", 2))
	assertStock(t, system, "src", "p1", 10, 0)
	must(t, system.CreateOrder("o2", "src", "dst", []Line{{Product: "p1", Quantity: 4}}, 3))
	must(t, system.ShipOrder("o2", 4))
	requireFailure(t, system.CancelOrder("o2", 5), KindAlreadyShipped)
	requireFailure(t, system.CancelOrder("missing", 6), KindOrderNotFound)
}

func TestReceiveToleranceFloorAndExactTolerance(t *testing.T) {
	config := Config{TolerancePerMille: 15, WaitDuration: 10}
	system := NewSystem(map[Warehouse]map[Product]Quantity{
		"src": {"p1": 100},
		"dst": {},
	}, config)
	must(t, system.CreateOrder("o1", "src", "dst", []Line{{Product: "p1", Quantity: 100}}, 1))
	must(t, system.ShipOrder("o1", 2))

	must(t, system.ReceiveLine("o1", 0, 100, 3))
	must(t, system.ReceiveLine("o1", 0, 1, 4))
	requireFailure(t, system.ReceiveLine("o1", 0, 1, 4), KindOverReceived)

	order, ok := system.Order("o1")
	if !ok || order.Lines[0].Received != 101 {
		t.Fatalf("order = %+v, ok=%v", order, ok)
	}
	assertStock(t, system, "dst", "p1", 101, 0)
	if !system.VerifyConservation() {
		t.Fatal("conservation failed at exact tolerance")
	}
}

func TestCloseWaitingBoundaryAndEarlyComplete(t *testing.T) {
	config := Config{TolerancePerMille: 0, WaitDuration: 10}
	system := newTwoWarehouseSystem(config)
	must(t, system.CreateOrder("partial", "src", "dst", []Line{{Product: "p1", Quantity: 4}}, 0))
	must(t, system.ShipOrder("partial", 10))
	must(t, system.ReceiveLine("partial", 0, 3, 11))
	requireFailure(t, system.CloseOrder("partial", 19), KindCloseTooEarly)
	must(t, system.CloseOrder("partial", 20))

	order, _ := system.Order("partial")
	if order.Status != StatusClosed || order.Lines[0].Shortage != 1 {
		t.Fatalf("partial order = %+v", order)
	}
	requireFailure(t, system.ReceiveLine("partial", 0, 1, 21), KindAlreadyClosed)

	must(t, system.CreateOrder("complete", "src", "dst", []Line{{Product: "p2", Quantity: 4}}, 22))
	must(t, system.ShipOrder("complete", 23))
	must(t, system.ReceiveLine("complete", 0, 4, 24))
	must(t, system.CloseOrder("complete", 25))
}

func TestShortageAndSurplusCoexistAndRecoveryBounds(t *testing.T) {
	config := Config{TolerancePerMille: 100, WaitDuration: 10}
	system := newTwoWarehouseSystem(config)
	must(t, system.CreateOrder("o1", "src", "dst", []Line{
		{Product: "p1", Quantity: 10},
		{Product: "p2", Quantity: 10},
	}, 0))
	must(t, system.ShipOrder("o1", 10))
	must(t, system.ReceiveLine("o1", 0, 7, 11))
	must(t, system.ReceiveLine("o1", 1, 11, 12))
	must(t, system.CloseOrder("o1", 20))

	order, _ := system.Order("o1")
	if order.Lines[0].Shortage != 3 || order.Lines[1].Surplus != 1 {
		t.Fatalf("differences = %+v", order.Lines)
	}
	requireFailure(t, system.RecoverShortage("o1", 1, 1, 21), KindNoShortage)
	requireFailure(t, system.RecoverShortage("o1", 0, 4, 22), KindRecoveryTooMuch)
	must(t, system.RecoverShortage("o1", 0, 3, 23))
	order, _ = system.Order("o1")
	if order.Lines[0].Shortage != 0 {
		t.Fatalf("shortage after recovery = %d", order.Lines[0].Shortage)
	}
	assertStock(t, system, "dst", "p1", 10, 0)
	if !system.VerifyConservation() {
		t.Fatal("conservation failed after recovery")
	}
}

func TestRejectionPriorityAndClock(t *testing.T) {
	system := newTwoWarehouseSystem(Config{WaitDuration: 10})
	requireFailure(t, system.ShipOrder("", 0), KindInvalidArgument)
	requireFailure(t, system.ShipOrder("missing", 0), KindOrderNotFound)

	must(t, system.CreateOrder("o1", "src", "dst", []Line{{Product: "p1", Quantity: 1}}, 5))
	requireFailure(t, system.ShipOrder("o1", 4), KindClockRollback)
	assertStock(t, system, "src", "p1", 9, 1)
	must(t, system.ShipOrder("o1", 5))
	requireFailure(t, system.ShipOrder("o1", 5), KindAlreadyShipped)
}

func TestInvalidCreateArguments(t *testing.T) {
	system := newTwoWarehouseSystem(Config{})
	valid := []Line{{Product: "p1", Quantity: 1}}
	requireFailure(t, system.CreateOrder("", "src", "dst", valid, 0), KindInvalidArgument)
	requireFailure(t, system.CreateOrder("o", "same", "same", valid, 0), KindInvalidArgument)
	requireFailure(t, system.CreateOrder("o", "src", "dst", nil, 0), KindInvalidArgument)
	requireFailure(t, system.CreateOrder("o", "src", "dst", []Line{{Product: "p1", Quantity: 1}, {Product: "p1", Quantity: 1}}, 0), KindInvalidArgument)
}

func newTwoWarehouseSystem(config Config) *System {
	return NewSystem(map[Warehouse]map[Product]Quantity{
		"src": {"p1": 10, "p2": 10},
		"dst": {"p1": 0, "p2": 0},
	}, config)
}

func assertStock(t *testing.T, system *System, warehouse Warehouse, product Product, available Quantity, frozen Quantity) {
	t.Helper()
	got := system.Stock(warehouse, product)
	if got.Available != available || got.Frozen != frozen {
		t.Fatalf("stock %s/%s = %+v, want available=%d frozen=%d", warehouse, product, got, available, frozen)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
