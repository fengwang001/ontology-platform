package settlement

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	system := newTestSystem(t, Config{BusinessDays: []int{1, 2}, MaxFailDays: 3, PenaltyBPS: 10}, []Account{
		{ID: "a", Cash: 1000},
		{ID: "b", Cash: 1000, Holdings: map[SecurityID]int64{"X": 1000}},
	})

	var waitGroup sync.WaitGroup
	for index := 0; index < 200; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			id := uint64(index + 1)
			input := OrderInput{
				ID:            id,
				Security:      "X",
				Buyer:         "a",
				Seller:        "b",
				Quantity:      1,
				Price:         1,
				SettlementDay: 1,
				AllowPartial:  true,
			}
			for attempt := 0; attempt < 10; attempt++ {
				if err := system.RegisterOrder(input); err == nil {
					return
				}
				time.Sleep(time.Microsecond)
			}
		}(index)
	}

	waitGroup.Wait()
	result, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 1})
	if err != nil {
		t.Fatalf("ProcessBusinessDay: %v", err)
	}
	if len(result.Processed) != 200 {
		t.Fatalf("processed %d orders, want 200", len(result.Processed))
	}
	for _, fill := range result.Processed {
		if fill.Delivered != 1 {
			t.Fatalf("order %d delivered = %d, want 1", fill.OrderID, fill.Delivered)
		}
	}
	assertPosition(t, system, "a", 800, map[SecurityID]int64{"X": 200})
	assertPosition(t, system, "b", 1200, map[SecurityID]int64{"X": 800})
}

func TestCashAndSecuritiesConservationAcrossRandomDay(t *testing.T) {
	system := newTestSystem(t, Config{BusinessDays: []int{1, 2}, MaxFailDays: 2, PenaltyBPS: 100}, []Account{
		{ID: "a", Cash: 100, Holdings: map[SecurityID]int64{"X": 3}},
		{ID: "b", Cash: 50, Holdings: map[SecurityID]int64{"X": 7, "Y": 4}},
		{ID: "c", Cash: 25, Holdings: map[SecurityID]int64{"Y": 6}},
	})

	orders := []OrderInput{
		{ID: 1, Security: "X", Buyer: "c", Seller: "b", Quantity: 4, Price: 5, SettlementDay: 1, AllowPartial: true},
		{ID: 2, Security: "Y", Buyer: "a", Seller: "c", Quantity: 10, Price: 3, SettlementDay: 1, AllowPartial: true},
		{ID: 3, Security: "X", Buyer: "a", Seller: "b", Quantity: 2, Price: 20, SettlementDay: 1},
	}
	for _, order := range orders {
		mustRegister(t, system, order)
	}

	assertConservation(t, system, 175, map[SecurityID]int64{"X": 10, "Y": 10})
	if _, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 8, "Y": 2}); err != nil {
		t.Fatalf("ProcessBusinessDay: %v", err)
	}
	assertConservation(t, system, 175, map[SecurityID]int64{"X": 10, "Y": 10})
	if _, err := system.ProcessBusinessDay(2, map[SecurityID]int64{"X": 8, "Y": 2}); err != nil {
		t.Fatalf("ProcessBusinessDay day 2: %v", err)
	}
	assertConservation(t, system, 175, map[SecurityID]int64{"X": 10, "Y": 10})
}

func TestProcessDayDoesNotScanHistoricalClosedOrders(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}

	buildSystem := func(historicalOrders int) *System {
		accounts := []Account{
			{ID: "a", Cash: 1_000_000},
			{ID: "b", Cash: 1_000_000, Holdings: map[SecurityID]int64{"X": 1_000_000}},
		}
		system, err := New(Config{BusinessDays: []int{1, 2}, MaxFailDays: 1, PenaltyBPS: 1}, accounts)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		system.SetLogger(discardLogger())
		for id := uint64(1); id <= uint64(historicalOrders); id++ {
			err := system.RegisterOrder(OrderInput{
				ID:            id,
				Security:      "X",
				Buyer:         "a",
				Seller:        "b",
				Quantity:      1,
				Price:         1,
				SettlementDay: 1,
			})
			if err != nil {
				t.Fatalf("RegisterOrder: %v", err)
			}
		}
		if _, err := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 1}); err != nil {
			t.Fatalf("ProcessBusinessDay historical: %v", err)
		}
		err = system.RegisterOrder(OrderInput{
			ID:            uint64(historicalOrders + 1),
			Security:      "X",
			Buyer:         "a",
			Seller:        "b",
			Quantity:      1,
			Price:         1,
			SettlementDay: 2,
			AllowPartial:  true,
		})
		if err != nil {
			t.Fatalf("RegisterOrder day 2: %v", err)
		}
		return system
	}

	largeSystem := buildSystem(2000)
	if len(largeSystem.byDue) != 1 || len(largeSystem.active) != 0 || largeSystem.dueHeap.Len() != 1 {
		t.Fatalf("historical closed orders remain in active scheduling structures: by_due=%d active=%d heap=%d", len(largeSystem.byDue), len(largeSystem.active), largeSystem.dueHeap.Len())
	}

	start := time.Now()
	if _, err := largeSystem.ProcessBusinessDay(2, map[SecurityID]int64{"X": 1}); err != nil {
		t.Fatalf("ProcessBusinessDay day 2: %v", err)
	}
	large := time.Since(start)
	t.Logf("day-2 processing with 2000 closed historical orders and one active order: %s", large)
	if large > time.Millisecond {
		t.Fatalf("day-2 processing unexpectedly slow with closed historical orders: %s", large)
	}
}

func assertConservation(t *testing.T, system *System, expectedCash int64, expectedSecurities map[SecurityID]int64) {
	t.Helper()
	totalCash := int64(0)
	totalSecurities := make(map[SecurityID]int64)

	for accountID := range system.accounts {
		position, err := system.Position(accountID)
		if err != nil {
			t.Fatalf("Position %s: %v", accountID, err)
		}
		totalCash += position.Cash
		for security, quantity := range position.Holdings {
			totalSecurities[security] += quantity
		}
		if position.Cash < 0 {
			t.Fatalf("account %s has negative cash", accountID)
		}
		for security, quantity := range position.Holdings {
			if quantity < 0 {
				t.Fatalf("account %s has negative %s", accountID, security)
			}
		}
	}

	if totalCash != expectedCash {
		t.Fatalf("total cash = %d, want %d", totalCash, expectedCash)
	}
	for security, expected := range expectedSecurities {
		if totalSecurities[security] != expected {
			t.Fatalf("total %s = %d, want %d", security, totalSecurities[security], expected)
		}
	}
}

func ExampleSystem_processBusinessDay() {
	system, _ := New(Config{BusinessDays: []int{1}, MaxFailDays: 2, PenaltyBPS: 100}, []Account{
		{ID: "buyer", Cash: 100},
		{ID: "seller", Holdings: map[SecurityID]int64{"X": 7}},
	})
	_ = system.RegisterOrder(OrderInput{ID: 1, Security: "X", Buyer: "buyer", Seller: "seller", Quantity: 10, Price: 10, SettlementDay: 1, AllowPartial: true})
	system.SetLogger(nil)
	result, _ := system.ProcessBusinessDay(1, map[SecurityID]int64{"X": 10})
	fmt.Println(result.Processed[0].Delivered, result.Penalties[0].Amount)
	// Output: 7 1
}
