package parking

import (
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	lot := newTestLot(t, "A", 1, 2, 10)
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 20; step++ {
				at := int64(worker*20 + step)
				vehicle := "v" + string(rune('a'+worker))
				id := vehicle + "-" + string(rune('a'+step))
				if _, err := lot.Reserve("A", vehicle, id, at, at+60, false, at); err == nil {
					_, _ = lot.CheckIn(id, at)
					_ = lot.Depart(id, at+30)
				}
			}
		}(worker)
	}
	wg.Wait()
	for second := int64(0); second < 300; second++ {
		count := 0
		for _, spot := range []int{1, 2, 10} {
			owner, err := lot.SpotOwner("A", spot, second)
			if err != nil {
				t.Fatal(err)
			}
			if owner.ReservationID != "" {
				count++
			}
		}
		if count > 3 {
			t.Fatalf("impossible owner count at %d", second)
		}
	}
}

func TestErrorPriorityAndRejectedClock(t *testing.T) {
	lot := newTestLot(t, "A", 1)
	reserve(t, lot, "r1", "v1", "A", 600, 900, false, 10)
	if _, err := lot.CheckIn("", 5); err != ErrInvalidArgument {
		t.Fatalf("invalid argument must precede clock rollback: %v", err)
	}
	if _, err := lot.CheckIn("missing", 5); err != ErrClockRollback {
		t.Fatalf("clock rollback must precede missing reservation: %v", err)
	}
	if _, err := lot.CheckIn("missing", 11); err != ErrReservationGone {
		t.Fatalf("missing reservation: %v", err)
	}
	if _, err := lot.CheckIn("r1", 539); err != ErrEarlyArrival {
		t.Fatalf("early arrival: %v", err)
	}
}
