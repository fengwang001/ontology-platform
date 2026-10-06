package expresshub

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentOperationsAndSnapshotConsistency(t *testing.T) {
	system, _ := New(Config{MaxItems: 10, MaxWeightGrams: 1000, DwellLimitSec: 1_000_000})
	const stations = 20
	const perStation = 10

	bagIDs := make(chan int64, stations*perStation)
	sealedIDs := make(chan int64, stations)
	var writers sync.WaitGroup
	var addWriters sync.WaitGroup
	var sealWriters sync.WaitGroup
	stop := make(chan struct{})
	var readers sync.WaitGroup

	for reader := 0; reader < 4; reader++ {
		readers.Add(1)
		go func(id int) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				destination := fmt.Sprintf("S%d", id%stations)
				view, ok, err := system.OpenBag(destination)
				if err != nil {
					t.Errorf("OpenBag() error = %v", err)
					return
				}
				if ok {
					assertBagSnapshot(t, view)
				}
				select {
				case bagID := <-bagIDs:
					bag, ok, err := system.Bag(bagID)
					if err != nil || !ok {
						t.Errorf("Bag(%d) ok=%v err=%v", bagID, ok, err)
						return
					}
					assertBagSnapshot(t, bag)
				default:
				}
			}
		}(reader)
	}

	for station := 0; station < stations; station++ {
		addWriters.Add(1)
		go func(station int) {
			defer addWriters.Done()
			destination := fmt.Sprintf("S%d", station)
			for item := 0; item < perStation; item++ {
				result, err := system.AddParcel(AddParcelInput{
					Waybill:     fmt.Sprintf("%s-w%d", destination, item),
					Destination: destination,
					WeightGrams: int64(item + 1),
					Category:    Category(item%3) + 1,
					At:          0,
				})
				if err != nil {
					t.Errorf("AddParcel() error = %v", err)
					return
				}
				bagIDs <- result.BagID
			}
		}(station)
	}
	addWriters.Wait()

	for station := 0; station < stations; station++ {
		sealWriters.Add(1)
		go func(station int) {
			defer sealWriters.Done()
			destination := fmt.Sprintf("S%d", station)
			sealed, err := system.SealBag(SealBagInput{Destination: destination, At: 1})
			if err != nil {
				t.Errorf("SealBag() error = %v", err)
				return
			}
			sealedIDs <- sealed.BagID
		}(station)
	}
	sealWriters.Wait()
	close(sealedIDs)

	for bagID := range sealedIDs {
		writers.Add(1)
		go func(bagID int64) {
			defer writers.Done()
			if _, err := system.DispatchBag(DispatchBagInput{BagID: bagID, VehicleID: "V", At: 2}); err != nil {
				t.Errorf("DispatchBag() error = %v", err)
			}
		}(bagID)
	}

	writers.Wait()
	close(stop)
	readers.Wait()
	close(bagIDs)

	if got := len(system.active); got != stations*perStation {
		t.Fatalf("active parcel count = %d, want %d", got, stations*perStation)
	}
	for station := 0; station < stations; station++ {
		destination := fmt.Sprintf("S%d", station)
		if _, ok, _ := system.OpenBag(destination); ok {
			t.Fatalf("destination %s still has an open bag", destination)
		}
	}
}

func TestBagSnapshotIsIndependent(t *testing.T) {
	system, _ := New(Config{MaxItems: 3, MaxWeightGrams: 10, DwellLimitSec: 100})
	addOrFail(t, system, AddParcelInput{"w1", "A", 1, CategoryNormal, 0})
	first, _, _ := system.Bag(1)
	first.Items[0].Waybill = "mutated"
	first.Items = append(first.Items, Parcel{Waybill: "mutated"})
	again, _, _ := system.Bag(1)
	if again.Items[0].Waybill != "w1" || len(again.Items) != 1 {
		t.Fatalf("snapshot mutation changed stored bag: %+v", again)
	}
}

func assertBagSnapshot(t *testing.T, bag BagView) {
	t.Helper()
	if len(bag.Items) > bag.MaxItems {
		t.Fatalf("bag %d has %d items, limit %d", bag.ID, len(bag.Items), bag.MaxItems)
	}
	var total int64
	for _, item := range bag.Items {
		total += item.WeightGrams
		if item.Destination != bag.Destination || item.BagID != bag.ID {
			t.Fatalf("bag %d contains foreign/mislinked item %+v", bag.ID, item)
		}
	}
	if total != bag.TotalWeight {
		t.Fatalf("bag %d snapshot total = %d, items sum = %d", bag.ID, bag.TotalWeight, total)
	}
	if bag.Status == BagStatusSealed && len(bag.Items) == 0 {
		t.Fatalf("sealed bag %d is empty", bag.ID)
	}
}
