package parking

import (
	"errors"
	"testing"
)

func testConfig() ZoneConfig {
	return ZoneConfig{EarlyWindow: 60, GraceWindow: 60, BaseRatePerMinute: 10,
		ChargingPerMinute: 20, OvertimePerMinute: 5, NoShowFee: 30, OccupationPenalty: 100}
}

func testSpots(numbers ...int) []Spot {
	spots := make([]Spot, 0, len(numbers))
	for _, number := range numbers {
		kind := NormalSpot
		if number >= 10 {
			kind = ChargingSpot
		}
		spots = append(spots, Spot{Number: number, Kind: kind})
	}
	return spots
}

func newTestLot(t *testing.T, zone string, numbers ...int) *Lot {
	t.Helper()
	lot := NewLot()
	if err := lot.AddZone(zone, testConfig(), testSpots(numbers...)); err != nil {
		t.Fatal(err)
	}
	return lot
}

func reserve(t *testing.T, lot *Lot, id, vehicle, zone string, start, end int64, charge bool, at int64) int {
	t.Helper()
	result, err := lot.Reserve(zone, vehicle, id, start, end, charge, at)
	if err != nil {
		t.Fatalf("Reserve %s: %v", id, err)
	}
	return result.SpotNumber
}

func TestAdjacentIntervalsAndWindowBoundaries(t *testing.T) {
	lot := newTestLot(t, "A", 1)
	first := reserve(t, lot, "r1", "v1", "A", 600, 900, false, 0)
	second := reserve(t, lot, "r2", "v2", "A", 900, 1200, false, 0)
	if first != 1 || second != 1 {
		t.Fatalf("adjacent reservations must use same spot: %d %d", first, second)
	}
	if _, err := lot.CheckIn("r1", 539); err != ErrEarlyArrival {
		t.Fatalf("at = early-1: %v", err)
	}
	if spot, err := lot.CheckIn("r1", 540); err != nil || spot != 1 {
		t.Fatalf("at = early boundary: spot=%d err=%v", spot, err)
	}
	if err := lot.Depart("r1", 899); err != nil {
		t.Fatal(err)
	}
	if _, err := lot.CheckIn("r2", 961); err != ErrReservationDead {
		t.Fatalf("at = grace+1: %v", err)
	}
	lot2 := newTestLot(t, "B", 1)
	reserve(t, lot2, "g1", "g", "B", 600, 900, false, 0)
	if spot, err := lot2.CheckIn("g1", 660); err != nil || spot != 1 {
		t.Fatalf("at = grace boundary: spot=%d err=%v", spot, err)
	}
}

func TestGraceExpiryAndWaitlistSameInstant(t *testing.T) {
	lot := newTestLot(t, "A", 1, 2)
	reserve(t, lot, "r1", "v1", "A", 600, 1200, false, 0)
	reserve(t, lot, "r2", "v2", "A", 600, 1200, false, 0)
	if err := lot.RegisterWaitlist("A", "v3", "w3", 660, 1200, false, 660); err != nil {
		t.Fatal(err)
	}
	promoted, err := lot.Reservation("w3")
	if err != nil || promoted.Status != StatusReserved || promoted.CreatedAt != 660 || promoted.Start != 660 {
		t.Fatalf("wait entry promotion: %+v %v", promoted, err)
	}
}

func TestOvertimeExactMinuteAndOneSecond(t *testing.T) {
	lot := newTestLot(t, "A", 1)
	reserve(t, lot, "r1", "v1", "A", 600, 660, false, 0)
	if _, err := lot.CheckIn("r1", 600); err != nil {
		t.Fatal(err)
	}
	if err := lot.Depart("r1", 720); err != nil {
		t.Fatal(err)
	}
	fee, err := lot.Fee("r1")
	if err != nil || fee.Overtime != 5 {
		t.Fatalf("exact minute fee: %+v %v", fee, err)
	}

	lot = newTestLot(t, "B", 1)
	reserve(t, lot, "r2", "v2", "B", 600, 660, false, 0)
	if _, err := lot.CheckIn("r2", 600); err != nil {
		t.Fatal(err)
	}
	if err := lot.Depart("r2", 721); err != nil {
		t.Fatal(err)
	}
	fee, _ = lot.Fee("r2")
	if fee.Overtime != 10 {
		t.Fatalf("one extra second should round up: %+v", fee)
	}
}

func TestReassignmentNormalExhaustedFallsBackToCharging(t *testing.T) {
	lot := newTestLot(t, "A", 1, 2, 10)
	old := reserve(t, lot, "old", "vo", "A", 600, 900, false, 0)
	next := reserve(t, lot, "next", "vn", "A", 900, 1200, false, 0)
	blocker := reserve(t, lot, "block", "vb", "A", 900, 1200, false, 0)
	if old != 1 || next != 1 || blocker != 2 {
		t.Fatalf("unexpected spots: old=%d next=%d blocker=%d", old, next, blocker)
	}
	if _, err := lot.CheckIn("old", 600); err != nil {
		t.Fatal(err)
	}
	spot, err := lot.CheckIn("next", 900)
	if err != nil || spot != 10 {
		t.Fatalf("expected charging fallback, got spot=%d err=%v", spot, err)
	}
	if err := lot.Depart("old", 930); err != nil {
		t.Fatal(err)
	}
	oldFee, _ := lot.Fee("old")
	nextFee, _ := lot.Fee("next")
	if oldFee.OccupationPenalty != 100 || nextFee.CompensationAward != 100 {
		t.Fatalf("penalty transfer mismatch: old=%+v next=%+v", oldFee, nextFee)
	}
}

func TestWaitlistEarlierIntervalMismatchThenLaterMatches(t *testing.T) {
	lot := newTestLot(t, "A", 1)
	reserve(t, lot, "r1", "v1", "A", 600, 1200, false, 0)
	reserve(t, lot, "r2", "v2", "A", 1200, 1500, false, 0)
	if err := lot.RegisterWaitlist("A", "long", "wl", 600, 1500, false, 0); err != nil {
		t.Fatal(err)
	}
	if err := lot.RegisterWaitlist("A", "short", "ws", 600, 900, false, 1); err != nil {
		t.Fatal(err)
	}
	if err := lot.Cancel("r1", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := lot.Reservation("wl"); !errors.Is(err, ErrReservationGone) {
		t.Fatalf("earlier full-period entry should remain queued")
	}
	short, err := lot.Reservation("ws")
	if err != nil || short.SpotNumber != 1 || short.CreatedAt != 10 {
		t.Fatalf("later matching entry should be promoted: %+v %v", short, err)
	}
}

func TestCancelAtStartAndOverlappingSecondCheckInRejected(t *testing.T) {
	lot := newTestLot(t, "A", 1)
	reserve(t, lot, "r1", "v", "A", 600, 900, false, 0)
	if err := lot.Cancel("r1", 600); err != nil {
		t.Fatal(err)
	}
	fee, _ := lot.Fee("r1")
	if fee.NoShow != 30 {
		t.Fatalf("cancel at start should charge no-show: %+v", fee)
	}

	lot = newTestLot(t, "B", 1)
	reserve(t, lot, "a1", "same", "B", 600, 1200, false, 0)
	reserve(t, lot, "a2", "same", "B", 1200, 1800, false, 0)
	if _, err := lot.CheckIn("a1", 600); err != nil {
		t.Fatal(err)
	}
	if _, err := lot.CheckIn("a2", 1200); !errors.Is(err, ErrVehicleOccupied) {
		t.Fatalf("second live check-in: %v", err)
	}
}
