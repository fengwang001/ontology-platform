package congestion

import (
	"slices"
	"sync"
	"time"
)

type plateAssignment struct {
	plate     string
	vehicleID string
	start     time.Time
}

type Service struct {
	mu           sync.RWMutex
	config       Config
	zones        map[string]*zoneNode
	entries      map[string][]Entry
	entitlements map[string][]Entitlement
	ledgers      map[string]map[string]*dayLedger
	assignments  []plateAssignment
	lastOp       time.Time
	nextAdjustID int64
}

func New(config Config) *Service {
	if config.Location == nil {
		config.Location = time.UTC
	}
	if config.DailyCap < 0 || config.MinorUnitDivisor < 0 || config.RetroactiveDays < 0 {
		panic(ErrInvalidArgument)
	}
	if config.MinorUnitDivisor == 0 {
		config.MinorUnitDivisor = 1
	}
	return &Service{
		config:       config,
		zones:        make(map[string]*zoneNode),
		entries:      make(map[string][]Entry),
		entitlements: make(map[string][]Entitlement),
		ledgers:      make(map[string]map[string]*dayLedger),
	}
}

func (s *Service) AddZone(config ZoneConfig, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.IsZero() {
		return ErrInvalidArgument
	}
	if err := validateZoneConfig(config); err != nil {
		return err
	}
	if _, exists := s.zones[config.ID]; exists {
		return ErrInvalidArgument
	}
	if config.ParentID != "" {
		if _, exists := s.zones[config.ParentID]; !exists {
			return ErrZoneNotFound
		}
	}
	if err := validateZoneNesting(config, s.zones); err != nil {
		return err
	}
	s.zones[config.ID] = &zoneNode{config: config}
	s.lastOp = at
	return nil
}

func (s *Service) RegisterVehicle(vehicleID, plate string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || plate == "" || at.IsZero() {
		return ErrInvalidArgument
	}
	if at.Before(s.lastOp) {
		return ErrClockMovedBack
	}
	if _, exists := s.entries[vehicleID]; exists {
		return ErrInvalidArgument
	}
	owner, _ := s.plateAt(plate, at)
	if owner != "" {
		return ErrInvalidArgument
	}
	s.entries[vehicleID] = nil
	s.assignments = append(s.assignments, plateAssignment{plate: plate, vehicleID: vehicleID, start: at})
	s.lastOp = at
	return nil
}

func (s *Service) ChangePlate(vehicleID, newPlate string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || newPlate == "" || at.IsZero() {
		return ErrInvalidArgument
	}
	if at.Before(s.lastOp) {
		return ErrClockMovedBack
	}
	if _, exists := s.entries[vehicleID]; !exists {
		return ErrVehicleNotFound
	}
	owner, _ := s.plateAt(newPlate, at)
	if owner != "" && owner != vehicleID {
		return ErrInvalidArgument
	}
	s.assignments = append(s.assignments, plateAssignment{plate: newPlate, vehicleID: vehicleID, start: at})
	s.lastOp = at
	return nil
}

func (s *Service) RecordEntry(plate, zoneID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if plate == "" || zoneID == "" || at.IsZero() {
		return ErrInvalidArgument
	}
	if at.Before(s.lastOp) {
		return ErrClockMovedBack
	}
	if _, exists := s.zones[zoneID]; !exists {
		return ErrZoneNotFound
	}
	vehicleID, _ := s.plateAt(plate, at)
	if vehicleID == "" {
		return ErrVehicleNotFound
	}
	entry := Entry{VehicleID: vehicleID, Plate: plate, ZoneID: zoneID, At: at}
	s.entries[vehicleID] = append(s.entries[vehicleID], entry)
	day := dayKey(at, s.config.Location)
	s.recompute(vehicleID, day, "", at, false)
	s.lastOp = at
	return nil
}

func (s *Service) RegisterEntitlement(vehicleID string, entitlement Entitlement, at time.Time) ([]Adjustment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || at.IsZero() || !entitlement.Start.Before(entitlement.End) {
		return nil, ErrInvalidArgument
	}
	if entitlement.Kind < Resident || entitlement.Kind > NewEnergy {
		return nil, ErrInvalidArgument
	}
	if entitlement.DiscountBasisPts < 0 || entitlement.DiscountBasisPts > 10000 {
		return nil, ErrInvalidArgument
	}
	if entitlement.Kind == Resident && entitlement.ZoneID == "" {
		return nil, ErrInvalidArgument
	}
	if at.Before(s.lastOp) {
		return nil, ErrClockMovedBack
	}
	if _, exists := s.zones[entitlement.ZoneID]; entitlement.Kind == Resident && !exists {
		return nil, ErrZoneNotFound
	}
	if _, exists := s.entries[vehicleID]; !exists {
		return nil, ErrVehicleNotFound
	}
	for _, existing := range s.entitlements[vehicleID] {
		if existing.Kind != entitlement.Kind {
			continue
		}
		if existing.Kind == Resident && existing.ZoneID != entitlement.ZoneID {
			continue
		}
		if existing.Start.Before(entitlement.End) && entitlement.Start.Before(existing.End) {
			return nil, ErrEntitlementOverlap
		}
	}
	entitlement.RegisteredAt = at
	affectedDays := map[string]struct{}{}
	for _, entry := range s.entries[vehicleID] {
		if !inInterval(entry.At, entitlement.Start, entitlement.End) {
			continue
		}
		day := dayKey(entry.At, s.config.Location)
		_, dayEnd, err := dayBounds(day, s.config.Location)
		if err != nil {
			return nil, err
		}
		if at.After(dayEnd.AddDate(0, 0, s.config.RetroactiveDays)) {
			return nil, ErrRetroactiveWindow
		}
		affectedDays[day] = struct{}{}
	}

	s.entitlements[vehicleID] = append(s.entitlements[vehicleID], entitlement)
	days := make([]string, 0, len(affectedDays))
	for day := range affectedDays {
		days = append(days, day)
	}
	slices.Sort(days)

	adjustments := make([]Adjustment, 0)
	for _, day := range days {
		ledger := s.ensureLedger(vehicleID, day)
		if ledger.frozen {
			continue
		}
		adjustment, changed := s.recompute(vehicleID, day, "retroactive entitlement", at, false)
		if changed {
			adjustments = append(adjustments, adjustment)
		}
	}
	s.lastOp = at
	return adjustments, nil
}

func (s *Service) OpenDispute(vehicleID string, day string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || day == "" || at.IsZero() {
		return ErrInvalidArgument
	}
	if at.Before(s.lastOp) {
		return ErrClockMovedBack
	}
	if _, _, err := dayBounds(day, s.config.Location); err != nil {
		return ErrInvalidArgument
	}
	if _, exists := s.entries[vehicleID]; !exists {
		return ErrVehicleNotFound
	}
	ledger := s.ensureLedger(vehicleID, day)
	if ledger.frozen {
		return ErrDisputeAlreadyExists
	}
	s.recompute(vehicleID, day, "", at, false)
	ledger = s.ensureLedger(vehicleID, day)
	ledger.frozen = true
	ledger.frozenAmount = ledger.payable
	s.lastOp = at
	return nil
}

func (s *Service) CloseDispute(vehicleID string, day string, at time.Time) (Adjustment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" || day == "" || at.IsZero() {
		return Adjustment{}, false, ErrInvalidArgument
	}
	if at.Before(s.lastOp) {
		return Adjustment{}, false, ErrClockMovedBack
	}
	if _, _, err := dayBounds(day, s.config.Location); err != nil {
		return Adjustment{}, false, ErrInvalidArgument
	}
	if _, exists := s.entries[vehicleID]; !exists {
		return Adjustment{}, false, ErrVehicleNotFound
	}
	ledger := s.ensureLedger(vehicleID, day)
	if !ledger.frozen {
		return Adjustment{}, false, ErrDisputeNotFound
	}
	before := ledger.frozenAmount
	ledger.frozen = false
	ledger.payable = before
	adjustment, changed := s.recompute(vehicleID, day, "dispute closed", at, true)
	ledger = s.ensureLedger(vehicleID, day)
	ledger.frozen = false
	ledger.frozenAmount = 0
	s.lastOp = at
	if !changed {
		return Adjustment{}, false, nil
	}
	return adjustment, true, nil
}

func (s *Service) Report(vehicleID string, day string) (DayReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if vehicleID == "" || day == "" {
		return DayReport{}, ErrInvalidArgument
	}
	if _, _, err := dayBounds(day, s.config.Location); err != nil {
		return DayReport{}, ErrInvalidArgument
	}
	if _, exists := s.entries[vehicleID]; !exists {
		return DayReport{}, ErrVehicleNotFound
	}
	ledger := s.ledgers[vehicleID][day]
	if ledger == nil {
		return DayReport{VehicleID: vehicleID, Day: day, Payable: 0, Adjustments: []Adjustment{}}, nil
	}
	payable := ledger.payable
	if ledger.frozen {
		payable = ledger.frozenAmount
	}
	return DayReport{
		VehicleID:   vehicleID,
		Day:         day,
		Payable:     payable,
		Adjustments: slices.Clone(ledger.adjustments),
		Frozen:      ledger.frozen,
		Evidence:    slices.Clone(ledger.evidenceLines()),
	}, nil
}

func (s *Service) plateAt(plate string, at time.Time) (string, time.Time) {
	for _, assignment := range s.assignments {
		if assignment.plate != plate || at.Before(assignment.start) {
			continue
		}
		active := true
		for _, other := range s.assignments {
			if other.vehicleID == assignment.vehicleID && other.start.After(assignment.start) && !other.start.After(at) {
				active = false
				break
			}
		}
		if active {
			return assignment.vehicleID, assignment.start
		}
	}
	return "", time.Time{}
}

func (s *Service) ensureLedger(vehicleID, day string) *dayLedger {
	if s.ledgers[vehicleID] == nil {
		s.ledgers[vehicleID] = make(map[string]*dayLedger)
	}
	if s.ledgers[vehicleID][day] == nil {
		s.ledgers[vehicleID][day] = &dayLedger{initialized: true}
	}
	return s.ledgers[vehicleID][day]
}

func (s *Service) recompute(vehicleID, day, reason string, at time.Time, force bool) (Adjustment, bool) {
	ledger := s.ensureLedger(vehicleID, day)
	before := ledger.payable
	if ledger.frozen && !force {
		return Adjustment{}, false
	}

	eligible := make([]Entitlement, 0, len(s.entitlements[vehicleID]))
	_, dayEnd, err := dayBounds(day, s.config.Location)
	if err != nil {
		return Adjustment{}, false
	}
	windowEnd := dayEnd.AddDate(0, 0, s.config.RetroactiveDays)
	for _, entitlement := range s.entitlements[vehicleID] {
		if !entitlement.RegisteredAt.After(windowEnd) {
			eligible = append(eligible, entitlement)
		}
	}

	payable, evidence, err := settleDay(vehicleID, day, s.entries[vehicleID], eligible, s.zones, s.config)
	if err != nil {
		return Adjustment{}, false
	}
	ledger.payable = payable
	ledger.setEvidence(evidence)
	if payable == before {
		return Adjustment{}, false
	}
	s.nextAdjustID++
	adjustment := Adjustment{
		ID:        s.nextAdjustID,
		VehicleID: vehicleID,
		Day:       day,
		Reason:    reason,
		At:        at,
		Before:    before,
		After:     payable,
		Delta:     payable - before,
		Evidence:  slices.Clone(evidence),
	}
	if reason != "" {
		ledger.adjustments = append(ledger.adjustments, adjustment)
	}
	return adjustment, true
}

func inInterval(at, start, end time.Time) bool {
	return !at.Before(start) && at.Before(end)
}
