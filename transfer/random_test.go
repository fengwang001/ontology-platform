package transfer

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type testOperation struct {
	name        string
	id          OrderID
	source      Warehouse
	destination Warehouse
	lines       []Line
	lineIndex   int
	quantity    Quantity
	at          Time
}

func randomOperation(random *rand.Rand, lastTime Time) testOperation {
	at := lastTime
	if random.Intn(5) > 0 {
		at += Time(random.Intn(4))
	}
	id := OrderID(fmt.Sprintf("o%d", random.Intn(8)))
	operation := testOperation{
		name:      []string{"create", "cancel", "ship", "receive", "close", "recover"}[random.Intn(6)],
		id:        id,
		lineIndex: random.Intn(2),
		quantity:  Quantity(1 + random.Intn(14)),
		at:        at,
	}
	if operation.name == "create" {
		operation.source = Warehouse([]string{"src", "dst", "bad"}[random.Intn(3)])
		operation.destination = Warehouse([]string{"dst", "src", "bad"}[random.Intn(3)])
		lineCount := 1 + random.Intn(2)
		operation.lines = make([]Line, lineCount)
		productChoices := []Product{"p0", "p1", "p0"}
		for index := range operation.lines {
			operation.lines[index] = Line{
				Product:  productChoices[random.Intn(len(productChoices))],
				Quantity: Quantity(1 + random.Intn(18)),
			}
			if index == 1 && random.Intn(3) == 0 {
				operation.lines[index].Product = operation.lines[0].Product
			}
		}
		if random.Intn(10) == 0 {
			operation.id = ""
		}
	}
	if random.Intn(12) == 0 {
		operation.lineIndex = -1
	}
	return operation
}

func applyOperation(system *System, op testOperation) error {
	switch op.name {
	case "create":
		return system.CreateOrder(op.id, op.source, op.destination, op.lines, op.at)
	case "cancel":
		return system.CancelOrder(op.id, op.at)
	case "ship":
		return system.ShipOrder(op.id, op.at)
	case "receive":
		return system.ReceiveLine(op.id, op.lineIndex, op.quantity, op.at)
	case "close":
		return system.CloseOrder(op.id, op.at)
	case "recover":
		return system.RecoverShortage(op.id, op.lineIndex, op.quantity, op.at)
	default:
		return ErrInvalidArgument
	}
}

func applyNaiveOperation(model *naiveSystem, op testOperation) naiveResult {
	switch op.name {
	case "create":
		return model.create(op.id, op.source, op.destination, op.lines, op.at)
	case "cancel":
		return model.cancel(op.id, op.at)
	case "ship":
		return model.ship(op.id, op.at)
	case "receive":
		return model.receive(op.id, op.lineIndex, op.quantity, op.at)
	case "close":
		return model.close(op.id, op.at)
	case "recover":
		return model.recover(op.id, op.lineIndex, op.quantity, op.at)
	default:
		return naiveResult{kind: KindInvalidArgument}
	}
}

func failureKind(err error) ErrorKind {
	var failure Failure
	if errors.As(err, &failure) {
		return failure.Kind
	}
	return ""
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed-%02d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			initial := map[Warehouse]map[Product]Quantity{
				"src": {"p0": 90, "p1": 90},
				"dst": {"p0": 0, "p1": 0},
			}
			config := Config{TolerancePerMille: uint64(random.Intn(120)), WaitDuration: uint64(random.Intn(8))}
			actual := NewSystem(initial, config)
			expected := newNaiveSystem(initial, config)
			lastTime := Time(0)

			for step := 0; step < 220; step++ {
				op := randomOperation(random, lastTime)
				lastTime = op.at
				err := applyOperation(actual, op)
				got := failureKind(err)
				want := applyNaiveOperation(expected, op)
				reason := want.reason
				if got == "" {
					reason = "accepted and state transitioned atomically"
				}
				actualConservation := actual.VerifyConservation()
				expectedConservation := expected.verify()
				t.Logf("step=%d op=%s id=%s src=%s dst=%s create_lines=%+v line=%d qty=%d at=%d output=%q basis=%s conservation actual=%v expected=%v",
					step, op.name, op.id, op.source, op.destination, op.lines, op.lineIndex, op.quantity, op.at, got, reason, actualConservation, expectedConservation)

				if got != want.kind {
					t.Fatalf("result mismatch: got %q (%v), want %q (%s)", got, err, want.kind, want.reason)
				}
				if !actualConservation || !expectedConservation {
					t.Fatalf("conservation mismatch after step %d: actual=%v expected=%v", step, actual.VerifyConservation(), expected.verify())
				}
				if err := assertModelOrdersMatch(actual, expected, step); err != nil {
					t.Fatal(err)
				}
				if err := assertModelStocksMatch(actual, expected, step); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("seed=%d config=%+v steps=220 result=pass basis=all error kinds, stocks, order rows and conservation matched naive model", seed, config)
		})
	}
}

func assertModelOrdersMatch(actual *System, expected *naiveSystem, step int) error {
	for id, expectedOrder := range expected.orders {
		got, exists := actual.Order(id)
		if !exists {
			return fmt.Errorf("step %d: actual missing order %s", step, id)
		}
		if got.Status != expectedOrder.status || got.Source != expectedOrder.source || got.Destination != expectedOrder.destination || got.ShippedAt != expectedOrder.shippedAt {
			return fmt.Errorf("step %d: order header mismatch %+v vs %+v", step, got, expectedOrder)
		}
		for index, expectedLine := range expectedOrder.lines {
			gotLine := got.Lines[index]
			if gotLine.Product != expectedLine.product ||
				gotLine.Quantity != expectedLine.quantity ||
				gotLine.Shipped != expectedLine.shipped ||
				gotLine.Received != expectedLine.received ||
				gotLine.Shortage != expectedLine.shortage ||
				gotLine.Surplus != expectedLine.surplus {
				return fmt.Errorf("step %d order %s line %d mismatch: %+v vs %+v", step, id, index, gotLine, expectedLine)
			}
		}
	}
	return nil
}

func assertModelStocksMatch(actual *System, expected *naiveSystem, step int) error {
	for key, expectedStock := range expected.stock {
		got := actual.Stock(key.warehouse, key.product)
		if got != expectedStock {
			return fmt.Errorf("step %d stock %s/%s mismatch: %+v vs %+v", step, key.warehouse, key.product, got, expectedStock)
		}
	}
	return nil
}
