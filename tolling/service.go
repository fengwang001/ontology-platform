package tolling

import (
	"slices"
	"sync"
	"time"
)

type storedRecord struct {
	record     GateRecord
	receivedAt time.Time
	duplicate  bool
	orphan     bool
	expired    bool
}

type trip struct {
	id          string
	vehicleID   string
	status      TripStatus
	entry       *storedRecord
	exit        *storedRecord
	effective   []storedRecord
	duplicates  []storedRecord
	orphans     []storedRecord
	expired     []storedRecord
	path        []string
	rawAmount   Money
	collected   Money
	refunded    Money
	uncollected Money
	adjustments []Adjustment
	lastError   ErrorCode
}

type Service struct {
	mu         sync.RWMutex
	cfg        Config
	network    *Network
	vehicles   map[string]*Vehicle
	trips      map[string]*trip
	pending    map[string][]storedRecord
	ledgers    map[string]*monthLedger
	acceptedAt map[string][]time.Time
	lastClock  time.Time
	nextAdjust int
}

func NewService(cfg Config, network *Network) *Service {
	if cfg.TimeZone == nil {
		cfg.TimeZone = time.UTC
	}
	if cfg.DuplicateWindow <= 0 || cfg.LateWindow < 0 || cfg.MonthlyCap < 0 {
		panic("invalid tolling config")
	}
	if network == nil {
		panic("nil tolling network")
	}
	return &Service{
		cfg:        cfg,
		network:    network,
		vehicles:   map[string]*Vehicle{},
		trips:      map[string]*trip{},
		pending:    map[string][]storedRecord{},
		ledgers:    map[string]*monthLedger{},
		acceptedAt: map[string][]time.Time{},
	}
}

func (s *Service) RegisterVehicle(vehicle Vehicle, at time.Time) error {
	if vehicle.ID == "" || vehicle.InitialClass == "" {
		return serviceError(InvalidArgument, "invalid vehicle")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	if _, ok := s.vehicles[vehicle.ID]; ok {
		return serviceError(InvalidArgument, "vehicle already exists")
	}
	copied := vehicle
	copied.ClassChanges = slices.Clone(vehicle.ClassChanges)
	s.vehicles[vehicle.ID] = &copied
	s.lastClock = at
	return nil
}

func (s *Service) ChangeVehicleClass(vehicleID, class string, effectiveAt, at time.Time) error {
	if vehicleID == "" || class == "" || effectiveAt.IsZero() || at.IsZero() {
		return serviceError(InvalidArgument, "invalid class change")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	vehicle, ok := s.vehicles[vehicleID]
	if !ok {
		return serviceError(VehicleNotFound, vehicleID)
	}
	vehicle.ClassChanges = append(vehicle.ClassChanges, VehicleClassChange{Class: class, EffectiveAt: effectiveAt})
	slices.SortFunc(vehicle.ClassChanges, func(a, b VehicleClassChange) int {
		return a.EffectiveAt.Compare(b.EffectiveAt)
	})
	s.lastClock = at
	return nil
}

func (s *Service) RegisterGate(record GateRecord, at time.Time) (OperationResult, error) {
	if record.VehicleID == "" || record.TripID == "" || record.GateID == "" || record.RecordedAt.IsZero() || at.IsZero() {
		return OperationResult{}, serviceError(InvalidArgument, "invalid gate record")
	}
	if record.Kind < Intermediate || record.Kind > Exit {
		return OperationResult{}, serviceError(InvalidArgument, "invalid record kind")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return OperationResult{}, err
	}
	if !s.network.HasGate(record.GateID) {
		return OperationResult{}, serviceError(GateNotFound, record.GateID)
	}
	vehicle, ok := s.vehicles[record.VehicleID]
	if !ok {
		return OperationResult{}, serviceError(VehicleNotFound, record.VehicleID)
	}
	_ = vehicle
	record.ReceivedAt = at
	stored := storedRecord{record: record, receivedAt: at}
	duplicate := s.isDuplicate(record)
	if duplicate {
		stored.duplicate = true
		if t := s.trips[record.TripID]; t != nil {
			t.duplicates = append(t.duplicates, stored)
		} else {
			s.pending[record.TripID] = append(s.pending[record.TripID], stored)
		}
		s.lastClock = at
		return OperationResult{Accepted: true, Duplicate: true, TripID: record.TripID}, nil
	}

	t := s.trips[record.TripID]
	if t == nil && record.Kind != Entry {
		if record.Kind == Exit {
			return OperationResult{}, serviceError(MissingEntry, record.TripID)
		}
		s.pending[record.TripID] = append(s.pending[record.TripID], stored)
		s.rememberGate(record)
		s.lastClock = at
		return OperationResult{Accepted: true, Orphan: true, TripID: record.TripID}, nil
	}

	if record.Kind == Entry {
		if t != nil {
			if t.status == TripSettledStatus {
				return OperationResult{}, serviceError(TripSettled, record.TripID)
			}
			return OperationResult{}, serviceError(InvalidArgument, "trip already has an entry")
		}
		t = &trip{id: record.TripID, vehicleID: record.VehicleID, status: TripOpen, entry: &stored}
		s.trips[record.TripID] = t
		s.rememberGate(record)
		s.lastClock = at
		for _, pending := range s.pending[record.TripID] {
			s.classifyPending(t, pending)
		}
		delete(s.pending, record.TripID)
		return OperationResult{Accepted: true, TripID: record.TripID}, nil
	}

	if t.vehicleID != record.VehicleID {
		return OperationResult{}, serviceError(InvalidArgument, "trip belongs to another vehicle")
	}
	s.rememberGate(record)

	if record.Kind == Exit {
		if t.status == TripSettledStatus {
			return OperationResult{}, serviceError(TripSettled, record.TripID)
		}
		exit := stored
		t.exit = &exit
		return s.settle(t, at)
	}

	if t.status == TripClosed {
		return OperationResult{}, serviceError(InvalidArgument, "trip is closed")
	}
	if t.status == TripSettledStatus {
		result, err := s.acceptLate(t, stored)
		if err == nil {
			s.lastClock = at
		}
		return result, err
	}
	s.classifyPending(t, stored)
	s.lastClock = at
	if t.status == TripFailed {
		return OperationResult{Accepted: true, TripID: t.id, Code: PathUnreachable}, nil
	}
	return OperationResult{Accepted: true, TripID: t.id}, nil
}

func (s *Service) RetrySettlement(tripID string, at time.Time) (OperationResult, error) {
	if tripID == "" || at.IsZero() {
		return OperationResult{}, serviceError(InvalidArgument, "invalid retry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return OperationResult{}, err
	}
	t, ok := s.trips[tripID]
	if !ok {
		return OperationResult{}, serviceError(TripNotFound, tripID)
	}
	if t.status == TripSettledStatus {
		return OperationResult{}, serviceError(TripSettled, tripID)
	}
	if t.exit == nil {
		return OperationResult{}, serviceError(MissingEntry, "exit record is missing")
	}
	return s.settle(t, at)
}

func (s *Service) CloseTrip(tripID string, at time.Time) error {
	if tripID == "" || at.IsZero() {
		return serviceError(InvalidArgument, "invalid close")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	t, ok := s.trips[tripID]
	if !ok {
		return serviceError(TripNotFound, tripID)
	}
	t.status = TripClosed
	s.lastClock = at
	return nil
}

func (s *Service) checkClock(at time.Time) error {
	if at.Before(s.lastClock) {
		return serviceError(ClockMovedBack, "operation is before last accepted operation")
	}
	return nil
}

func gateKey(vehicleID, tripID, gateID string) string {
	return vehicleID + "\x00" + tripID + "\x00" + gateID
}

func (s *Service) isDuplicate(record GateRecord) bool {
	for _, accepted := range s.acceptedAt[gateKey(record.VehicleID, record.TripID, record.GateID)] {
		diff := record.RecordedAt.Sub(accepted)
		if diff < 0 {
			diff = -diff
		}
		if diff < s.cfg.DuplicateWindow {
			return true
		}
	}
	return false
}

func (s *Service) rememberGate(record GateRecord) {
	key := gateKey(record.VehicleID, record.TripID, record.GateID)
	s.acceptedAt[key] = append(s.acceptedAt[key], record.RecordedAt)
	slices.SortFunc(s.acceptedAt[key], func(a, b time.Time) int { return a.Compare(b) })
}

func (s *Service) classifyPending(t *trip, stored storedRecord) {
	entryAt := t.entry.record.RecordedAt
	if stored.record.RecordedAt.Before(entryAt) {
		stored.orphan = true
		t.orphans = append(t.orphans, stored)
		return
	}
	if t.exit != nil && stored.record.RecordedAt.After(t.exit.record.RecordedAt) {
		stored.orphan = true
		t.orphans = append(t.orphans, stored)
		return
	}
	t.effective = append(t.effective, stored)
}

func (s *Service) acceptLate(t *trip, stored storedRecord) (OperationResult, error) {
	entryAt := t.entry.record.RecordedAt
	exitAt := t.exit.record.RecordedAt
	recordedAt := stored.record.RecordedAt
	if recordedAt.Before(entryAt) || recordedAt.After(exitAt) {
		stored.orphan = true
		t.orphans = append(t.orphans, stored)
		return OperationResult{Accepted: true, Orphan: true, TripID: t.id}, nil
	}
	if stored.receivedAt.Sub(exitAt) > s.cfg.LateWindow {
		stored.expired = true
		t.expired = append(t.expired, stored)
		return OperationResult{Accepted: true, TripID: t.id, Code: InvalidArgument}, nil
	}
	t.effective = append(t.effective, stored)
	path, raw, err := s.infer(t)
	if err != nil {
		t.effective = t.effective[:len(t.effective)-1]
		return OperationResult{}, err
	}
	return s.applyRecomputation(t, path, raw, stored.receivedAt)
}

func (s *Service) settle(t *trip, at time.Time) (OperationResult, error) {
	exitAt := t.exit.record.RecordedAt
	entryAt := t.entry.record.RecordedAt
	kept := t.effective[:0]
	for _, item := range t.effective {
		if !item.record.RecordedAt.Before(entryAt) && !item.record.RecordedAt.After(exitAt) {
			kept = append(kept, item)
		} else {
			item.orphan = true
			t.orphans = append(t.orphans, item)
		}
	}
	t.effective = kept
	path, raw, err := s.infer(t)
	if err != nil {
		t.status = TripFailed
		t.lastError = PathUnreachable
		return OperationResult{Accepted: true, TripID: t.id, Code: PathUnreachable}, err
	}
	result, err := s.applyRecomputation(t, path, raw, at)
	if err != nil {
		t.status = TripFailed
		t.lastError = PathUnreachable
	}
	return result, err
}

func (s *Service) infer(t *trip) ([]string, Money, error) {
	vehicle := s.vehicles[t.vehicleID]
	anchors := make([]anchor, 0, len(t.effective)+2)
	anchors = append(anchors, anchor{t.entry.record.GateID, vehicle.ClassAt(t.entry.record.RecordedAt)})
	items := slices.Clone(t.effective)
	slices.SortFunc(items, func(a, b storedRecord) int {
		if cmp := a.record.RecordedAt.Compare(b.record.RecordedAt); cmp != 0 {
			return cmp
		}
		if a.record.GateID < b.record.GateID {
			return -1
		}
		if a.record.GateID > b.record.GateID {
			return 1
		}
		return 0
	})
	for _, item := range items {
		anchors = append(anchors, anchor{item.record.GateID, vehicle.ClassAt(item.record.RecordedAt)})
	}
	if t.exit != nil {
		anchors = append(anchors, anchor{t.exit.record.GateID, vehicle.ClassAt(t.exit.record.RecordedAt)})
	}
	path, raw, err := s.network.Reconstruct(anchors)
	if err != nil {
		return nil, 0, err
	}
	return path, raw, nil
}

func (s *Service) applyRecomputation(t *trip, path []string, raw Money, at time.Time) (OperationResult, error) {
	previous := t.collected
	ledger := s.monthLedger(t)
	result := ledger.apply(raw, previous, t.uncollected, s.cfg.MonthlyCap)
	delta := raw - t.rawAmount
	kind := InitialCharge
	reason := "initial settlement"
	if len(t.adjustments) != 0 {
		switch {
		case delta > 0:
			kind = Supplementary
			reason = "late record increased inferred amount"
		case delta < 0:
			kind = Refund
			reason = "late record decreased inferred amount"
		default:
			kind = NoAdjustment
			reason = "late record produced equal amount"
		}
	}
	adjustment := Adjustment{
		ID:            s.nextAdjust,
		At:            at,
		Kind:          kind,
		RawDelta:      delta,
		CashCharge:    result.charge,
		CashRefund:    result.refund,
		Amount:        result.charge + result.refund,
		RefundBlocked: result.blockedRefund,
		CappedBefore:  t.uncollected,
		CappedAfter:   result.uncollected,
		Reason:        reason,
	}
	if kind == Refund {
		adjustment.Amount = result.refund
	}
	if kind != NoAdjustment {
		adjustment.ID = s.nextAdjust
		s.nextAdjust++
		t.adjustments = append(t.adjustments, adjustment)
	}
	t.path = slices.Clone(path)
	t.rawAmount = raw
	t.collected += result.charge - result.refund
	t.refunded += result.refund
	t.uncollected = result.uncollected
	t.status = TripSettledStatus
	t.lastError = ""
	s.lastClock = at
	view := OperationResult{
		Accepted:  true,
		TripID:    t.id,
		Path:      slices.Clone(path),
		RawAmount: raw,
		Collected: t.collected,
	}
	if kind != NoAdjustment {
		view.Adjustment = &t.adjustments[len(t.adjustments)-1]
	} else {
		view.Adjustment = &adjustment
	}
	return view, nil
}

func (s *Service) monthLedger(t *trip) *monthLedger {
	key := t.vehicleID + "\x00" + monthKey(t.exit.record.RecordedAt, s.cfg.TimeZone)
	ledger, ok := s.ledgers[key]
	if !ok {
		ledger = &monthLedger{}
		s.ledgers[key] = ledger
	}
	return ledger
}

func (s *Service) Trip(tripID string) (TripView, error) {
	if tripID == "" {
		return TripView{}, serviceError(InvalidArgument, "empty trip id")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.trips[tripID]
	if !ok {
		return TripView{}, serviceError(TripNotFound, tripID)
	}
	view := TripView{
		TripID:            t.id,
		VehicleID:         t.vehicleID,
		Status:            t.status,
		Path:              slices.Clone(t.path),
		RawAmount:         t.rawAmount,
		Collected:         t.collected,
		Refunded:          t.refunded,
		CappedUncollected: t.uncollected,
		Adjustments:       slices.Clone(t.adjustments),
	}
	if t.entry != nil {
		view.EntryGate = t.entry.record.GateID
		view.EntryAt = t.entry.record.RecordedAt
	}
	if t.exit != nil {
		view.ExitGate = t.exit.record.GateID
		view.ExitAt = t.exit.record.RecordedAt
		view.CurrentMonth = monthKey(t.exit.record.RecordedAt, s.cfg.TimeZone)
	}
	return view, nil
}

func (s *Service) Adjustments(tripID string) ([]Adjustment, error) {
	view, err := s.Trip(tripID)
	if err != nil {
		return nil, err
	}
	if view.Status != TripSettledStatus {
		return nil, serviceError(TripNotSettled, tripID)
	}
	return slices.Clone(view.Adjustments), nil
}

func (s *Service) Records(tripID string) ([]RecordAudit, error) {
	if tripID == "" {
		return nil, serviceError(InvalidArgument, "empty trip id")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, ok := s.trips[tripID]; ok {
		var records []storedRecord
		if t.entry != nil {
			records = append(records, *t.entry)
		}
		if t.exit != nil {
			records = append(records, *t.exit)
		}
		records = append(records, t.effective...)
		records = append(records, t.duplicates...)
		records = append(records, t.orphans...)
		records = append(records, t.expired...)
		return audits(records), nil
	}
	if pending, ok := s.pending[tripID]; ok {
		return audits(pending), nil
	}
	return nil, serviceError(TripNotFound, tripID)
}

func audits(records []storedRecord) []RecordAudit {
	out := make([]RecordAudit, 0, len(records))
	for _, item := range records {
		out = append(out, RecordAudit{
			Record:      item.record,
			Duplicate:   item.duplicate,
			Orphan:      item.orphan,
			ExpiredLate: item.expired,
			Effective:   !item.duplicate && !item.orphan && !item.expired,
		})
	}
	return out
}

func (s *Service) Month(vehicleID, month string) (MonthView, error) {
	if vehicleID == "" || len(month) != 7 {
		return MonthView{}, serviceError(InvalidArgument, "invalid month query")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	view := MonthView{VehicleID: vehicleID, Month: month}
	if ledger := s.ledgers[vehicleID+"\x00"+month]; ledger != nil {
		view.Collected = ledger.collected
		view.CappedCharges = ledger.cappedCharges
		view.BlockedRefunds = ledger.blockedRefunds
	}
	return view, nil
}
