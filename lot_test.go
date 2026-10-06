package parking

import (
	"errors"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{VacateMinutes: 10, FreeMinutes: 10, BillingUnit: 15, UnitFee: 5, DailyCap: 20, OverTimeRate: 2, GraceDays: 2}
}

func TestShareMidnightAndEndpoints(t *testing.T) {
	m := NewManager(1, testConfig())
	spot, err := m.RegisterMonthly(0, "owner", 0, 10000)
	if err != nil || spot != 0 {
		t.Fatalf("register = %d, %v", spot, err)
	}
	if err := m.SetShare(0, "owner", 0, Interval{Start: 1430, End: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.VisitorEnter(1429, "a"); err != ErrNoSpace {
		t.Fatalf("before shared: %v", err)
	}
	if _, err := m.VisitorEnter(1430, "b"); err != nil {
		t.Fatalf("left closed: %v", err)
	}
	if _, err := m.VisitorEnter(1450, "c"); err != ErrNoSpace {
		t.Fatalf("public priority occupied: %v", err)
	}
	if _, err := m.Exit(1450, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.VisitorEnter(1450, "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Exit(Day+20, "d"); err != nil {
		t.Fatalf("right open exit at end: %v", err)
	}
	if _, err := m.VisitorEnter(Day+20, "e"); err != ErrNoSpace {
		t.Fatalf("right closed: %v", err)
	}
}

func TestFreeEndpointAndRoundingAndDailyCap(t *testing.T) {
	m := NewManager(1, testConfig())
	if _, err := m.VisitorEnter(0, "v"); err != nil {
		t.Fatal(err)
	}
	result, err := m.Exit(10, "v")
	if err != nil || result.Fee != 0 {
		t.Fatalf("exact free: %+v %v", result, err)
	}
	m = NewManager(1, testConfig())
	_, _ = m.VisitorEnter(0, "v")
	result, _ = m.Exit(25, "v")
	if result.Fee != 5 {
		t.Fatalf("15 minutes = one unit, got %+v", result)
	}
	m = NewManager(1, testConfig())
	_, _ = m.VisitorEnter(0, "v")
	result, _ = m.Exit(26, "v")
	if result.Fee != 10 {
		t.Fatalf("16 minutes rounds to two units, got %+v", result)
	}
	m = NewManager(1, testConfig())
	_, _ = m.VisitorEnter(0, "v")
	result, _ = m.Exit(Day+30, "v")
	if result.Fee != 30 {
		t.Fatalf("daily caps should add, got %+v", result)
	}
}

func TestVacateEndpointsWaitingAndAutoReturn(t *testing.T) {
	m := NewManager(2, testConfig())
	ownerSpot, _ := m.RegisterMonthly(0, "owner", 0, 10000)
	_ = m.SetShare(0, "owner", ownerSpot, Interval{Start: 0, End: 720})
	_, _ = m.VisitorEnter(0, "pub")
	visitorSpot, _ := m.VisitorEnter(100, "v")
	if visitorSpot != ownerSpot {
		t.Fatalf("visitor spot = %d", visitorSpot)
	}
	ownerSpotReturn, err := m.MonthlyEnter(100, "owner")
	if err != nil || ownerSpotReturn != -1 {
		t.Fatalf("owner waiting = %d, %v", ownerSpotReturn, err)
	}
	result, err := m.Exit(110, "v")
	if err != nil || result.Fee != 0 {
		t.Fatalf("exact vacate is free: %+v %v", result, err)
	}
	home := m.spots[ownerSpot]
	if home.Occupied != "owner" {
		t.Fatalf("owner = %q", home.Occupied)
	}

	_, _ = m.Exit(200, "owner")
	_, _ = m.Exit(200, "pub")
	_, _ = m.VisitorEnter(300, "pub")
	_, _ = m.VisitorEnter(300, "v2")
	ownerSpotReturn, err = m.MonthlyEnter(300, "owner")
	if err != nil || ownerSpotReturn != -1 {
		t.Fatalf("owner waiting = %d, %v", ownerSpotReturn, err)
	}
	result, _ = m.Exit(311, "v2")
	if result.Fee != 2 {
		t.Fatalf("one overtime minute = 2, got %+v", result)
	}
	if m.spots[ownerSpot].Occupied != "owner" {
		t.Fatal("waiting owner did not auto-return")
	}
}

func TestShareEndOverTime(t *testing.T) {
	m := NewManager(1, testConfig())
	spot, _ := m.RegisterMonthly(0, "owner", 0, 10000)
	if err := m.SetShare(0, "owner", spot, Interval{Start: 0, End: 100}); err != nil {
		t.Fatal(err)
	}
	_, _ = m.VisitorEnter(0, "v")
	result, _ := m.Exit(111, "v")
	if result.Fee != 42 {
		t.Fatalf("90 base minutes capped + 11 overtime, got %+v", result)
	}
}

func TestLeaseExpirationEntryAndGraceLastDay(t *testing.T) {
	m := NewManager(2, testConfig())
	_, _ = m.RegisterMonthly(0, "owner", 0, Day)
	if _, err := m.MonthlyEnter(Day-1, "owner"); err != nil {
		t.Fatalf("last in-lease day: %v", err)
	}
	_, _ = m.Exit(Day, "owner")
	if _, err := m.MonthlyEnter(Day, "owner"); !errors.Is(err, ErrLease) {
		t.Fatalf("expired day: %v", err)
	}
	if _, err := m.VisitorEnter(Day, "owner"); err != nil {
		t.Fatalf("expired owner enters as visitor: %v", err)
	}
	_, _ = m.Exit(Day+1, "owner")
	lastGrace := 3*Day - 1
	if err := m.RenewMonthly(lastGrace, "owner", 10*Day); err != nil {
		t.Fatalf("last grace day: %v", err)
	}
	if _, err := m.MonthlyEnter(lastGrace, "owner"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectionOrderAndNoTrace(t *testing.T) {
	m := NewManager(3, testConfig())
	_, _ = m.RegisterMonthly(0, "owner", 0, 10000)
	if _, err := m.VisitorEnter(-1, "v"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid first: %v", err)
	}
	_, _ = m.VisitorEnter(5, "v")
	if _, err := m.VisitorEnter(4, "v"); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("clock before not-found: %v", err)
	}
	if err := m.SetShare(5, "other", 0, Interval{Start: 0, End: 10}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found before state: %v", err)
	}
	_, _ = m.VisitorEnter(5, "visitor")
	before := len(m.Snapshot(5))
	if _, err := m.VisitorEnter(6, "visitor"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("already inside: %v", err)
	}
	if m.lastNow != 5 || len(m.Snapshot(5)) != before {
		t.Fatal("rejected operation changed clock or registry")
	}
}

func TestConcurrentAllocationAndExitLinearizable(t *testing.T) {
	m := NewManager(8, testConfig())
	for owner := 0; owner < 4; owner++ {
		spot, err := m.RegisterMonthly(0, string(rune('a'+owner)), 0, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.SetShare(0, string(rune('a'+owner)), spot, Interval{Start: 0, End: 720}); err != nil {
			t.Fatal(err)
		}
	}
	var wait sync.WaitGroup
	assigned := make(chan int, 16)
	for i := 0; i < 16; i++ {
		wait.Add(1)
		i := i
		go func() {
			defer wait.Done()
			spot, err := m.VisitorEnter(1, "v"+string(rune('0'+i)))
			if err == nil {
				assigned <- spot
			}
		}()
	}
	wait.Wait()
	close(assigned)
	seen := map[int]bool{}
	for spot := range assigned {
		if seen[spot] {
			t.Fatalf("spot %d assigned twice", spot)
		}
		seen[spot] = true
	}
	if len(seen) != 8 {
		t.Fatalf("accepted = %d", len(seen))
	}
}
