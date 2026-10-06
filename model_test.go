package congestion

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

type naiveState struct {
	location    *time.Location
	cap         int64
	retroDays   int
	zones       map[string]ZoneConfig
	entries     map[string][]Entry
	ent         map[string][]Entitlement
	assignments []plateAssignment
	disputes    map[string]map[string]bool
	payable     map[string]map[string]int64
	adjustments map[string]map[string][]Adjustment
	last        time.Time
	nextID      int64
}

func newNaiveState() *naiveState {
	location := time.UTC
	state := &naiveState{
		location:    location,
		cap:         45,
		retroDays:   2,
		zones:       make(map[string]ZoneConfig),
		entries:     make(map[string][]Entry),
		ent:         make(map[string][]Entitlement),
		disputes:    make(map[string]map[string]bool),
		payable:     make(map[string]map[string]int64),
		adjustments: make(map[string]map[string][]Adjustment),
	}
	state.zones["outer"] = ZoneConfig{ID: "outer", Fee: 30, Start: TimeOfDay{8, 0}, End: TimeOfDay{18, 0}}
	state.zones["inner"] = ZoneConfig{ID: "inner", ParentID: "outer", Fee: 20, Start: TimeOfDay{8, 0}, End: TimeOfDay{18, 0}}
	return state
}

func (m *naiveState) vehicle(plate string, at time.Time) string {
	vehicle := ""
	start := time.Time{}
	for _, assignment := range m.assignments {
		if assignment.plate == plate && !at.Before(assignment.start) && assignment.start.After(start) {
			vehicle = assignment.vehicleID
			start = assignment.start
		}
	}
	return vehicle
}

func (m *naiveState) frozen(vehicle, day string) bool {
	return m.disputes[vehicle] != nil && m.disputes[vehicle][day]
}

func (m *naiveState) changePlate(vehicle, plate string, at time.Time) error {
	if vehicle == "" || plate == "" || at.IsZero() {
		return ErrInvalidArgument
	}
	if at.Before(m.last) {
		return ErrClockMovedBack
	}
	if _, ok := m.entries[vehicle]; !ok {
		return ErrVehicleNotFound
	}
	for _, assignment := range m.assignments {
		if assignment.plate == plate && !at.Before(assignment.start) && assignment.vehicleID != vehicle {
			return ErrInvalidArgument
		}
	}
	m.assignments = append(m.assignments, plateAssignment{vehicleID: vehicle, plate: plate, start: at})
	m.last = at
	return nil
}

func (m *naiveState) entry(plate, zoneID string, at time.Time) error {
	if plate == "" || zoneID == "" || at.IsZero() {
		return ErrInvalidArgument
	}
	if at.Before(m.last) {
		return ErrClockMovedBack
	}
	if _, ok := m.zones[zoneID]; !ok {
		return ErrZoneNotFound
	}
	vehicle := m.vehicle(plate, at)
	if vehicle == "" {
		return ErrVehicleNotFound
	}
	m.entries[vehicle] = append(m.entries[vehicle], Entry{VehicleID: vehicle, Plate: plate, ZoneID: zoneID, At: at})
	m.recompute(vehicle, dayKey(at, m.location), "", at)
	m.last = at
	return nil
}

func (m *naiveState) register(vehicle string, candidate Entitlement, at time.Time) ([]Adjustment, error) {
	if vehicle == "" || at.IsZero() || !candidate.Start.Before(candidate.End) ||
		candidate.Kind < Resident || candidate.Kind > NewEnergy ||
		candidate.DiscountBasisPts < 0 || candidate.DiscountBasisPts > 10000 ||
		(candidate.Kind == Resident && candidate.ZoneID == "") {
		return nil, ErrInvalidArgument
	}
	if at.Before(m.last) {
		return nil, ErrClockMovedBack
	}
	if candidate.Kind == Resident {
		if _, ok := m.zones[candidate.ZoneID]; !ok {
			return nil, ErrZoneNotFound
		}
	}
	if _, ok := m.entries[vehicle]; !ok {
		return nil, ErrVehicleNotFound
	}
	for _, old := range m.ent[vehicle] {
		if old.Kind != candidate.Kind || (old.Kind == Resident && old.ZoneID != candidate.ZoneID) {
			continue
		}
		if old.Start.Before(candidate.End) && candidate.Start.Before(old.End) {
			return nil, ErrEntitlementOverlap
		}
	}
	days := map[string]struct{}{}
	for _, event := range m.entries[vehicle] {
		if event.At.Before(candidate.Start) || !event.At.Before(candidate.End) {
			continue
		}
		day := dayKey(event.At, m.location)
		_, end, err := dayBounds(day, m.location)
		if err != nil {
			return nil, err
		}
		if at.After(end.AddDate(0, 0, m.retroDays)) {
			return nil, ErrRetroactiveWindow
		}
		days[day] = struct{}{}
	}
	candidate.RegisteredAt = at
	m.ent[vehicle] = append(m.ent[vehicle], candidate)
	ordered := make([]string, 0, len(days))
	for day := range days {
		ordered = append(ordered, day)
	}
	sort.Strings(ordered)
	created := make([]Adjustment, 0)
	for _, day := range ordered {
		if m.frozen(vehicle, day) {
			continue
		}
		if adjustment := m.recompute(vehicle, day, "retroactive entitlement", at); adjustment != nil {
			created = append(created, *adjustment)
		}
	}
	m.last = at
	return created, nil
}

func (m *naiveState) open(vehicle, day string, at time.Time) error {
	if vehicle == "" || day == "" || at.IsZero() {
		return ErrInvalidArgument
	}
	if at.Before(m.last) {
		return ErrClockMovedBack
	}
	if _, _, err := dayBounds(day, m.location); err != nil {
		return ErrInvalidArgument
	}
	if _, ok := m.entries[vehicle]; !ok {
		return ErrVehicleNotFound
	}
	if m.frozen(vehicle, day) {
		return ErrDisputeAlreadyExists
	}
	m.recompute(vehicle, day, "", at)
	if m.disputes[vehicle] == nil {
		m.disputes[vehicle] = make(map[string]bool)
	}
	m.disputes[vehicle][day] = true
	m.last = at
	return nil
}

func (m *naiveState) close(vehicle, day string, at time.Time) (Adjustment, bool, error) {
	if vehicle == "" || day == "" || at.IsZero() {
		return Adjustment{}, false, ErrInvalidArgument
	}
	if at.Before(m.last) {
		return Adjustment{}, false, ErrClockMovedBack
	}
	if _, _, err := dayBounds(day, m.location); err != nil {
		return Adjustment{}, false, ErrInvalidArgument
	}
	if _, ok := m.entries[vehicle]; !ok {
		return Adjustment{}, false, ErrVehicleNotFound
	}
	if !m.frozen(vehicle, day) {
		return Adjustment{}, false, ErrDisputeNotFound
	}
	m.disputes[vehicle][day] = false
	adjustment := m.recompute(vehicle, day, "dispute closed", at)
	m.last = at
	if adjustment == nil {
		return Adjustment{}, false, nil
	}
	return *adjustment, true, nil
}

func (m *naiveState) recompute(vehicle, day, reason string, at time.Time) *Adjustment {
	if m.frozen(vehicle, day) {
		return nil
	}
	start, end, err := dayBounds(day, m.location)
	if err != nil {
		return nil
	}
	events := append([]Entry(nil), m.entries[vehicle]...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })

	type firstEntry struct {
		at    time.Time
		order int
	}
	first := make(map[string]firstEntry)
	for order, event := range events {
		if event.At.Before(start) || !event.At.Before(end) {
			continue
		}
		zoneIDs := []string{event.ZoneID}
		if event.ZoneID == "inner" {
			zoneIDs = append(zoneIDs, "outer")
		}
		for _, zoneID := range zoneIDs {
			if _, exists := first[zoneID]; exists {
				continue
			}
			zone := m.zones[zoneID]
			local := event.At.In(m.location)
			seconds := local.Hour()*3600 + local.Minute()*60 + local.Second()
			windowStart := zone.Start.Hour*3600 + zone.Start.Minute*60
			windowEnd := zone.End.Hour*3600 + zone.End.Minute*60
			if seconds >= windowStart && seconds < windowEnd {
				first[zoneID] = firstEntry{at: event.At, order: order}
			}
		}
	}

	charged := make([]string, 0, len(first))
	for zoneID := range first {
		charged = append(charged, zoneID)
	}
	sort.Slice(charged, func(i, j int) bool {
		left, right := first[charged[i]], first[charged[j]]
		if left.at.Equal(right.at) {
			return left.order < right.order
		}
		return left.at.Before(right.at)
	})

	applies := func(kind EntitlementKind, zoneID string, at time.Time) bool {
		for _, entitlement := range m.ent[vehicle] {
			if entitlement.RegisteredAt.After(end.AddDate(0, 0, m.retroDays)) {
				continue
			}
			if entitlement.Kind != kind || at.Before(entitlement.Start) || !at.Before(entitlement.End) {
				continue
			}
			if kind == Resident && entitlement.ZoneID != zoneID {
				continue
			}
			return true
		}
		return false
	}
	disabled := false
	for _, zoneID := range charged {
		disabled = disabled || applies(Disabled, zoneID, first[zoneID].at)
	}

	total := int64(0)
	for _, zoneID := range charged {
		zone := m.zones[zoneID]
		charge := zone.Fee
		when := first[zoneID].at
		switch {
		case disabled:
			charge = 0
		case applies(NewEnergy, zoneID, when):
			charge = 0
		case applies(Resident, zoneID, when):
			rate := int64(10000)
			for _, entitlement := range m.ent[vehicle] {
				if entitlement.Kind == Resident && entitlement.ZoneID == zoneID &&
					!when.Before(entitlement.Start) && when.Before(entitlement.End) {
					rate = 10000 - entitlement.DiscountBasisPts
				}
			}
			charge = (zone.Fee*rate + 9999) / 10000
		}
		if total+charge > m.cap {
			charge = m.cap - total
		}
		total += charge
	}

	if m.payable[vehicle] == nil {
		m.payable[vehicle] = make(map[string]int64)
		m.adjustments[vehicle] = make(map[string][]Adjustment)
	}
	before := m.payable[vehicle][day]
	m.payable[vehicle][day] = total
	if total == before || reason == "" {
		return nil
	}
	m.nextID++
	adjustment := Adjustment{
		ID:        m.nextID,
		VehicleID: vehicle,
		Day:       day,
		Reason:    reason,
		At:        at,
		Before:    before,
		After:     total,
		Delta:     total - before,
	}
	m.adjustments[vehicle][day] = append(m.adjustments[vehicle][day], adjustment)
	return &adjustment
}

func TestRandomOperationsMatchNaiveReplay(t *testing.T) {
	random := rand.New(rand.NewSource(1543))
	service := newNestedService(t, 45)
	model := newNaiveState()
	base := mustTime(t, "2026-01-01T07:00:00Z")
	vehicles := []string{"v0", "v1", "v2"}
	plates := []string{"P0", "P1", "P2"}
	currentPlates := map[string]string{}
	for i := range vehicles {
		registeredAt := base.Add(time.Duration(i) * time.Second)
		if err := service.RegisterVehicle(vehicles[i], plates[i], registeredAt); err != nil {
			t.Fatal(err)
		}
		model.entries[vehicles[i]] = nil
		model.assignments = append(model.assignments, plateAssignment{
			vehicleID: vehicles[i],
			plate:     plates[i],
			start:     registeredAt,
		})
		currentPlates[vehicles[i]] = plates[i]
	}
	model.last = base.Add(2 * time.Second)

	compare := func(step int, action string) {
		t.Helper()
		for _, vehicle := range vehicles {
			for day := 2; day <= 5; day++ {
				dayText := fmt.Sprintf("2026-01-%02d", day)
				report, err := service.Report(vehicle, dayText)
				if err != nil {
					t.Fatal(err)
				}
				want := int64(0)
				if model.payable[vehicle] != nil {
					want = model.payable[vehicle][dayText]
				}
				frozen := model.frozen(vehicle, dayText)
				if report.Payable != want || report.Frozen != frozen {
					t.Fatalf("step %d after %s: %s %s service payable=%d frozen=%v, naive payable=%d frozen=%v\nevidence=%v",
						step, action, vehicle, dayText, report.Payable, report.Frozen, want, frozen, report.Evidence)
				}
				gotAdjustments := report.Adjustments
				wantAdjustments := []Adjustment(nil)
				if model.adjustments[vehicle] != nil {
					wantAdjustments = model.adjustments[vehicle][dayText]
				}
				if len(gotAdjustments) != len(wantAdjustments) {
					t.Fatalf("step %d %s %s %s adjustments service=%+v naive=%+v", step, action, vehicle, dayText, gotAdjustments, wantAdjustments)
				}
				for index := range gotAdjustments {
					got := gotAdjustments[index]
					wantAdjustment := wantAdjustments[index]
					if got.Delta != wantAdjustment.Delta || got.Reason != wantAdjustment.Reason ||
						got.Before != wantAdjustment.Before || got.After != wantAdjustment.After {
						t.Fatalf("step %d adjustment mismatch: service=%+v naive=%+v", step, got, wantAdjustment)
					}
				}
			}
		}
	}

	sameError := func(a, b error) bool {
		if a == nil || b == nil {
			return a == b
		}
		return errors.Is(a, b) || errors.Is(b, a) || a.Error() == b.Error()
	}

	for step := 0; step < 260; step++ {
		vehicle := vehicles[random.Intn(len(vehicles))]
		day := 2 + random.Intn(3)
		hour := 7 + random.Intn(11)
		minute := random.Intn(60)
		at := time.Date(2026, 1, day, hour, minute, 0, 0, time.UTC)
		if at.Before(model.last) {
			at = model.last.Add(time.Duration(random.Intn(60)) * time.Minute)
		}
		choice := random.Intn(9)
		switch choice {
		case 0:
			plate := fmt.Sprintf("Q%d", step)
			serviceErr := service.ChangePlate(vehicle, plate, at)
			naiveErr := model.changePlate(vehicle, plate, at)
			t.Logf("step=%d action=change_plate input vehicle=%s plate=%s at=%s output service=%v naive=%v decision=%t",
				step, vehicle, plate, at.Format(time.RFC3339), serviceErr, naiveErr, serviceErr == nil)
			if !sameError(serviceErr, naiveErr) {
				t.Fatalf("change plate errors service=%v naive=%v", serviceErr, naiveErr)
			}
			if serviceErr == nil {
				currentPlates[vehicle] = plate
			}
		case 1, 2, 3:
			zoneID := []string{"outer", "inner"}[random.Intn(2)]
			plate := currentPlates[vehicle]
			serviceErr := service.RecordEntry(plate, zoneID, at)
			naiveErr := model.entry(plate, zoneID, at)
			t.Logf("step=%d action=entry input plate=%s zone=%s at=%s output service=%v naive=%v decision=%t",
				step, plate, zoneID, at.Format(time.RFC3339), serviceErr, naiveErr, serviceErr == nil)
			if !sameError(serviceErr, naiveErr) {
				t.Fatalf("entry errors service=%v naive=%v", serviceErr, naiveErr)
			}
		case 4, 5:
			kind := []EntitlementKind{Disabled, NewEnergy, Resident}[random.Intn(3)]
			start := at.Add(-time.Duration(random.Intn(12)+1) * time.Hour)
			end := at.Add(time.Duration(random.Intn(12)+1) * time.Hour)
			candidate := Entitlement{Kind: kind, Start: start, End: end, DiscountBasisPts: int64(random.Intn(6) * 2000)}
			if kind == Resident {
				candidate.ZoneID = []string{"outer", "inner"}[random.Intn(2)]
			}
			serviceAdjustments, serviceErr := service.RegisterEntitlement(vehicle, candidate, at)
			naiveAdjustments, naiveErr := model.register(vehicle, candidate, at)
			t.Logf("step=%d action=entitlement input vehicle=%s kind=%d zone=%s [%s,%s) registered_at=%s output service_adjustments=%+v service_err=%v naive_adjustments=%+v naive_err=%v decision=%t",
				step, vehicle, kind, candidate.ZoneID, start.Format(time.RFC3339), end.Format(time.RFC3339),
				at.Format(time.RFC3339), serviceAdjustments, serviceErr, naiveAdjustments, naiveErr, serviceErr == nil)
			if !sameError(serviceErr, naiveErr) || len(serviceAdjustments) != len(naiveAdjustments) {
				t.Fatalf("entitlement mismatch service=%+v,%v naive=%+v,%v", serviceAdjustments, serviceErr, naiveAdjustments, naiveErr)
			}
		case 6:
			dayText := fmt.Sprintf("2026-01-%02d", day)
			serviceErr := service.OpenDispute(vehicle, dayText, at)
			naiveErr := model.open(vehicle, dayText, at)
			t.Logf("step=%d action=open_dispute input vehicle=%s day=%s at=%s output service=%v naive=%v decision=%t",
				step, vehicle, dayText, at.Format(time.RFC3339), serviceErr, naiveErr, serviceErr == nil)
			if !sameError(serviceErr, naiveErr) {
				t.Fatalf("open errors service=%v naive=%v", serviceErr, naiveErr)
			}
		case 7:
			dayText := fmt.Sprintf("2026-01-%02d", day)
			serviceAdjustment, serviceChanged, serviceErr := service.CloseDispute(vehicle, dayText, at)
			naiveAdjustment, naiveChanged, naiveErr := model.close(vehicle, dayText, at)
			t.Logf("step=%d action=close_dispute input vehicle=%s day=%s at=%s output service=(%+v,%t,%v) naive=(%+v,%t,%v)",
				step, vehicle, dayText, at.Format(time.RFC3339), serviceAdjustment, serviceChanged, serviceErr,
				naiveAdjustment, naiveChanged, naiveErr)
			if !sameError(serviceErr, naiveErr) || serviceChanged != naiveChanged ||
				(serviceChanged && serviceAdjustment.Delta != naiveAdjustment.Delta) {
				t.Fatalf("close mismatch service=%+v,%t,%v naive=%+v,%t,%v", serviceAdjustment, serviceChanged, serviceErr, naiveAdjustment, naiveChanged, naiveErr)
			}
		default:
			earlier := model.last.Add(-time.Nanosecond)
			serviceErr := service.RecordEntry(currentPlates[vehicle], "outer", earlier)
			t.Logf("step=%d action=clock_rollback input vehicle=%s at=%s output err=%v decision=rejected",
				step, vehicle, earlier.Format(time.RFC3339Nano), serviceErr)
			if !errors.Is(serviceErr, ErrClockMovedBack) {
				t.Fatalf("clock rollback error = %v", serviceErr)
			}
		}
		compare(step, fmt.Sprintf("choice=%d", choice))
	}
}
