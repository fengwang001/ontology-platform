package transfer

import (
	"sync"
	"testing"
)

func TestConcurrentLifecycleAndSnapshots(t *testing.T) {
	const orders = 64
	initial := map[Warehouse]map[Product]Quantity{
		"src": {"p0": orders, "p1": orders},
		"dst": {},
	}
	system := NewSystem(initial, Config{TolerancePerMille: 100, WaitDuration: 0})

	for index := 0; index < orders; index++ {
		id := OrderID(rune('a' + index))
		if err := system.CreateOrder(id, "src", "dst", []Line{{Product: "p0", Quantity: 1}}, 0); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	var start sync.WaitGroup
	start.Add(1)
	var workers sync.WaitGroup
	for index := 0; index < orders; index++ {
		id := OrderID(rune('a' + index))
		workers.Add(1)
		go func(id OrderID) {
			defer workers.Done()
			start.Wait()
			if err := system.ShipOrder(id, 1); err != nil {
				t.Errorf("ship %s: %v", id, err)
				return
			}
			if err := system.ReceiveLine(id, 0, 1, 1); err != nil {
				t.Errorf("receive %s: %v", id, err)
				return
			}
			if err := system.CloseOrder(id, 1); err != nil {
				t.Errorf("close %s: %v", id, err)
			}
		}(id)
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		start.Wait()
		for attempt := 0; attempt < orders*4; attempt++ {
			snapshot := system.Stock("src", "p0")
			if snapshot.Available+snapshot.Frozen > orders {
				t.Errorf("inconsistent source snapshot: %+v", snapshot)
				return
			}
			system.VerifyConservation()
		}
	}()

	start.Done()
	workers.Wait()

	finalSource := system.Stock("src", "p0")
	finalDestination := system.Stock("dst", "p0")
	if finalSource.Available != 0 || finalSource.Frozen != 0 || finalDestination.Available != orders {
		t.Fatalf("final stocks source=%+v destination=%+v", finalSource, finalDestination)
	}
	if !system.VerifyConservation() {
		t.Fatal("conservation failed after concurrent lifecycle")
	}
}
