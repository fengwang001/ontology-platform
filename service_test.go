package congestion

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func newNestedService(t *testing.T, cap int64) *Service {
	t.Helper()
	service := New(Config{Location: time.UTC, DailyCap: cap, MinorUnitDivisor: 1, RetroactiveDays: 2})
	outer := ZoneConfig{
		ID:     "outer",
		Bounds: Rect{0, 0, 10, 10},
		Fee:    30,
		Start:  TimeOfDay{8, 0},
		End:    TimeOfDay{18, 0},
	}
	inner := ZoneConfig{
		ID:       "inner",
		ParentID: "outer",
		Bounds:   Rect{2, 2, 8, 8},
		Fee:      20,
		Start:    TimeOfDay{8, 0},
		End:      TimeOfDay{18, 0},
	}
	if err := service.AddZone(outer, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := service.AddZone(inner, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	return service
}

func registerTestVehicle(t *testing.T, service *Service, vehicle, plate string, at time.Time) {
	t.Helper()
	if err := service.RegisterVehicle(vehicle, plate, at); err != nil {
		t.Fatal(err)
	}
}

func assertReport(t *testing.T, service *Service, vehicle, day string, want int64) DayReport {
	t.Helper()
	report, err := service.Report(vehicle, day)
	if err != nil {
		t.Fatal(err)
	}
	if report.Payable != want {
		t.Fatalf("payable = %d, want %d: %v", report.Payable, want, report.Evidence)
	}
	return report
}

func containsAll(value string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func TestChargeWindowEndpoints(t *testing.T) {
	service := newNestedService(t, 100)
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
	day := "2026-01-02"
	if err := service.RecordEntry("A", "outer", mustTime(t, "2026-01-02T08:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntry("A", "outer", mustTime(t, "2026-01-02T18:00:00Z")); err != nil {
		t.Fatal(err)
	}
	assertReport(t, service, "v", day, 30)
}

func TestNestedSameMinuteBothOrders(t *testing.T) {
	for _, innerFirst := range []bool{true, false} {
		service := newNestedService(t, 100)
		registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
		at := mustTime(t, "2026-01-02T09:00:00Z")
		if innerFirst {
			registerTestVehicle(t, service, "v2", "B", mustTime(t, "2026-01-01T07:01:00Z"))
			if err := service.RecordEntry("A", "inner", at); err != nil {
				t.Fatal(err)
			}
			if err := service.RecordEntry("B", "outer", at); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := service.RecordEntry("A", "outer", at); err != nil {
				t.Fatal(err)
			}
			if err := service.RecordEntry("A", "inner", at); err != nil {
				t.Fatal(err)
			}
		}
		assertReport(t, service, "v", "2026-01-02", 50)
	}
}

func TestDailyCapExact(t *testing.T) {
	service := newNestedService(t, 50)
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
	if err := service.RecordEntry("A", "inner", mustTime(t, "2026-01-02T09:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntry("A", "inner", mustTime(t, "2026-01-02T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	report := assertReport(t, service, "v", "2026-01-02", 50)
	if len(report.Adjustments) != 0 {
		t.Fatalf("cap reached by charges must not create adjustments: %+v", report.Adjustments)
	}
}

func TestAllThreeEntitlementsDisabledWins(t *testing.T) {
	service := newNestedService(t, 100)
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
	at := mustTime(t, "2026-01-02T09:00:00Z")
	for _, entitlement := range []Entitlement{
		{Kind: Resident, ZoneID: "outer", Start: at, End: at.Add(time.Hour), DiscountBasisPts: 5000},
		{Kind: NewEnergy, Start: at, End: at.Add(time.Hour)},
		{Kind: Disabled, Start: at, End: at.Add(time.Hour)},
	} {
		if _, err := service.RegisterEntitlement("v", entitlement, mustTime(t, "2026-01-01T07:01:00Z")); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.RecordEntry("A", "inner", at); err != nil {
		t.Fatal(err)
	}
	report := assertReport(t, service, "v", "2026-01-02", 0)
	if len(report.Adjustments) != 0 {
		t.Fatalf("highest-priority entitlement must suppress lower-priority traces, got %+v", report.Adjustments)
	}
	for _, line := range report.Evidence {
		if line == "" || (containsAll(line, []string{"resident", "new energy"})) {
			t.Fatalf("suppressed entitlement appears in evidence: %q", line)
		}
	}
}

func TestDiscountRoundedThenCap(t *testing.T) {
	service := New(Config{Location: time.UTC, DailyCap: 10, MinorUnitDivisor: 10})
	if err := service.AddZone(ZoneConfig{
		ID:     "z",
		Bounds: Rect{0, 0, 1, 1},
		Fee:    15,
		Start:  TimeOfDay{8, 0},
		End:    TimeOfDay{18, 0},
	}, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
	at := mustTime(t, "2026-01-02T09:00:00Z")
	_, err := service.RegisterEntitlement("v", Entitlement{
		Kind:             Resident,
		ZoneID:           "z",
		Start:            at,
		End:              at.Add(time.Hour),
		DiscountBasisPts: 5000,
	}, mustTime(t, "2026-01-01T07:01:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntry("A", "z", at); err != nil {
		t.Fatal(err)
	}
	assertReport(t, service, "v", "2026-01-02", 10)
}

func TestRetroactiveRegistrationAtDeadline(t *testing.T) {
	service := newNestedService(t, 100)
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
	registerTestVehicle(t, service, "v2", "B", mustTime(t, "2026-01-01T07:01:00Z"))
	entryAt := mustTime(t, "2026-01-02T08:00:00Z")
	if err := service.RecordEntry("A", "outer", entryAt); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntry("B", "outer", entryAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	deadline := mustTime(t, "2026-01-05T00:00:00Z")
	adjustments, err := service.RegisterEntitlement("v", Entitlement{
		Kind:  Disabled,
		Start: entryAt,
		End:   entryAt.Add(time.Hour),
	}, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if len(adjustments) != 1 || adjustments[0].Delta != -30 {
		t.Fatalf("deadline adjustment = %+v", adjustments)
	}
	afterDeadline := deadline.Add(time.Nanosecond)
	_, err = service.RegisterEntitlement("v2", Entitlement{
		Kind:  Disabled,
		Start: entryAt.Add(time.Minute),
		End:   entryAt.Add(time.Hour),
	}, afterDeadline)
	if !errors.Is(err, ErrRetroactiveWindow) {
		t.Fatalf("error = %v, want retroactive window", err)
	}
	assertReport(t, service, "v2", "2026-01-02", 30)
}

func TestPlateChangeAtEntryTime(t *testing.T) {
	service := newNestedService(t, 100)
	registerTestVehicle(t, service, "v", "OLD", mustTime(t, "2026-01-01T07:00:00Z"))
	at := mustTime(t, "2026-01-02T09:00:00Z")
	if err := service.ChangePlate("v", "NEW", at); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntry("NEW", "outer", at); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntry("OLD", "outer", at.Add(time.Minute)); !errors.Is(err, ErrVehicleNotFound) {
		t.Fatalf("old plate error = %v, want vehicle not found", err)
	}
	assertReport(t, service, "v", "2026-01-02", 30)
}

func TestRetroactiveEntitlementDuringFrozenDispute(t *testing.T) {
	service := newNestedService(t, 100)
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
	entryAt := mustTime(t, "2026-01-02T09:00:00Z")
	if err := service.RecordEntry("A", "inner", entryAt); err != nil {
		t.Fatal(err)
	}
	if err := service.OpenDispute("v", "2026-01-02", entryAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	adjustments, err := service.RegisterEntitlement("v", Entitlement{
		Kind:  Disabled,
		Start: entryAt,
		End:   entryAt.Add(2 * time.Hour),
	}, entryAt.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(adjustments) != 0 {
		t.Fatalf("frozen day adjustment = %+v", adjustments)
	}
	frozen := assertReport(t, service, "v", "2026-01-02", 50)
	if !frozen.Frozen || len(frozen.Adjustments) != 0 {
		t.Fatalf("frozen report changed: %+v", frozen)
	}
	if err := service.OpenDispute("v", "2026-01-02", entryAt.Add(3*time.Hour)); !errors.Is(err, ErrDisputeAlreadyExists) {
		t.Fatalf("duplicate dispute error = %v", err)
	}
	adjustment, changed, err := service.CloseDispute("v", "2026-01-02", entryAt.Add(4*time.Hour))
	if err != nil || !changed {
		t.Fatalf("close dispute changed=%v err=%v", changed, err)
	}
	if adjustment.Delta != -50 || adjustment.Reason != "dispute closed" {
		t.Fatalf("closing adjustment = %+v", adjustment)
	}
	report := assertReport(t, service, "v", "2026-01-02", 0)
	if len(report.Adjustments) != 1 {
		t.Fatalf("frozen-period changes must collapse into one adjustment: %+v", report.Adjustments)
	}
}

func TestClockRejectionDoesNotMutate(t *testing.T) {
	service := newNestedService(t, 100)
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2026-01-01T07:00:00Z"))
	at := mustTime(t, "2026-01-02T09:00:00Z")
	if err := service.RecordEntry("A", "outer", at); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntry("A", "outer", at.Add(-time.Nanosecond)); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("clock error = %v", err)
	}
	assertReport(t, service, "v", "2026-01-02", 30)
}

func TestIntersectingButNotNestedZoneRejected(t *testing.T) {
	service := newNestedService(t, 100)
	err := service.AddZone(ZoneConfig{
		ID:     "crossing",
		Bounds: Rect{8, 8, 12, 12},
		Fee:    10,
		Start:  TimeOfDay{8, 0},
		End:    TimeOfDay{18, 0},
	}, time.Unix(2, 0))
	if !errors.Is(err, ErrInvalidNesting) {
		t.Fatalf("error = %v, want invalid nesting", err)
	}
	if _, ok := service.zones["crossing"]; ok {
		t.Fatal("rejected zone mutated service state")
	}
}

func TestReportDoesNotScanVehicleHistory(t *testing.T) {
	service := newNestedService(t, 100)
	registerTestVehicle(t, service, "v", "A", mustTime(t, "2025-12-31T07:00:00Z"))
	for day := 1; day <= 30; day++ {
		for entry := 0; entry < 10; entry++ {
			at := time.Date(2026, 1, day, 9, entry, 0, 0, time.UTC)
			if err := service.RecordEntry("A", "outer", at); err != nil {
				t.Fatal(err)
			}
		}
	}
	report, err := service.Report("v", "2026-01-30")
	if err != nil {
		t.Fatal(err)
	}
	if report.Payable != 30 {
		t.Fatalf("payable = %d, want 30", report.Payable)
	}
	if len(report.Evidence) > 4 {
		t.Fatalf("evidence lines scan %d day entries; should stay bounded by one day", len(report.Evidence))
	}
}
