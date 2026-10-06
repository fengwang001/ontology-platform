package parking

import "strings"

func validConfig(cfg Config) bool {
	return cfg.VacateMinutes >= 0 && cfg.FreeMinutes >= 0 && cfg.BillingUnit > 0 &&
		cfg.UnitFee >= 0 && cfg.DailyCap >= 0 && cfg.OverTimeRate >= 0 && cfg.GraceDays > 0
}

func NewManager(spots int, cfg Config) *Manager {
	if spots <= 0 || !validConfig(cfg) {
		panic(ErrInvalidArgument)
	}
	manager := &Manager{
		cfg:        cfg,
		spots:      make([]*spotState, spots),
		leases:     make(map[string]*Lease),
		vehicles:   make(map[string]*vehicleState),
		publicFree: newMinIDSet(),
		sharedFree: newMinIDSet(),
		events:     &eventHeap{},
	}
	for id := 0; id < spots; id++ {
		spot := &spotState{ID: id, Public: true}
		manager.spots[id] = spot
		manager.publicFree.Add(id)
	}
	return manager
}

func (m *Manager) begin(now int) error {
	if now < 0 {
		return ErrInvalidArgument
	}
	if now < m.lastNow {
		return ErrClockRolledBack
	}
	m.processEvents(now)
	m.lastNow = now
	return nil
}

func (m *Manager) commit(now int) {
	m.processEvents(now)
	m.lastNow = now
}

func (m *Manager) RegisterMonthly(now int, plate string, start, end int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || strings.TrimSpace(plate) == "" || !validLeaseRange(start, end) {
		return 0, ErrInvalidArgument
	}
	if now < m.lastNow {
		return 0, ErrClockRolledBack
	}
	if _, exists := m.leases[plate]; exists {
		return 0, ErrInvalidState
	}
	m.commit(now)
	id, ok := m.publicFree.Min()
	if !ok {
		return 0, ErrNoSpace
	}
	lease := &Lease{Plate: plate, Spot: id, Start: start, End: end, GraceTo: end + m.cfg.GraceDays*Day}
	spot := m.spots[id]
	spot.Public = false
	spot.Owner = plate
	spot.Generation++
	m.publicFree.Remove(id)
	m.leases[plate] = lease
	if leaseActive(lease, now) && lease.HasShare {
		m.refreshSpotAvailability(spot, now)
	}
	m.scheduleLease(lease)
	return id, nil
}

func (m *Manager) RenewMonthly(now int, plate string, end int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || strings.TrimSpace(plate) == "" || end <= now {
		return ErrInvalidArgument
	}
	if now < m.lastNow {
		return ErrClockRolledBack
	}
	lease := m.leases[plate]
	if lease == nil {
		return ErrNotFound
	}
	if !canRenew(lease, now) {
		return ErrLease
	}
	m.commit(now)
	lease.End = end
	lease.GraceTo = end + m.cfg.GraceDays*Day
	m.scheduleLease(lease)
	return nil
}

func (m *Manager) SetShare(now int, ownerPlate string, spot int, interval Interval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || strings.TrimSpace(ownerPlate) == "" || !validTimeRange(interval.Start, interval.End) {
		return ErrInvalidArgument
	}
	if now < m.lastNow {
		return ErrClockRolledBack
	}
	if spot < 0 || spot >= len(m.spots) {
		return ErrNotFound
	}
	lease := m.leases[ownerPlate]
	if lease == nil {
		return ErrNotFound
	}
	if lease.Spot != spot {
		return ErrInvalidState
	}
	m.commit(now)
	lease.Share = interval
	lease.HasShare = true
	m.refreshSpotAvailability(m.spots[spot], now)
	m.scheduleShare(lease)
	return nil
}

func (m *Manager) Snapshot(now int) Registry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return nil
	}
	m.processEvents(now)
	m.lastNow = now
	result := make(Registry, len(m.leases))
	for plate, lease := range m.leases {
		result[plate] = *lease
	}
	return result
}

func (m *Manager) refreshSpotAvailability(spot *spotState, now int) {
	if spot.Occupied != "" || spot.Owner == "" {
		m.sharedFree.Remove(spot.ID)
		return
	}
	lease := m.leases[spot.Owner]
	if leaseActive(lease, now) && lease.HasShare && intervalOpen(lease, now) {
		m.sharedFree.Add(spot.ID)
	} else {
		m.sharedFree.Remove(spot.ID)
	}
}

func (m *Manager) scheduleLease(lease *Lease) {
	spot := m.spots[lease.Spot]
	generation := spot.Generation
	m.events.push(scheduledEvent{at: lease.Start, kind: eventLeaseStart, spot: spot.ID, generation: generation})
	m.events.push(scheduledEvent{at: lease.End, kind: eventLeaseEnd, spot: spot.ID, generation: generation})
	m.events.push(scheduledEvent{at: lease.GraceTo, kind: eventGraceEnd, spot: spot.ID, generation: generation})
}

func (m *Manager) scheduleShare(lease *Lease) {
	bound, ok := nextIntervalBound(lease, m.lastNow)
	if !ok || bound <= m.lastNow {
		return
	}
	kind := byte(eventShareStart)
	if intervalOpen(lease, m.lastNow) {
		kind = eventShareEnd
	}
	m.events.push(scheduledEvent{at: bound, kind: kind, spot: lease.Spot, generation: m.spots[lease.Spot].Generation})
}

func (m *Manager) processEvents(now int) {
	for m.events.Len() > 0 && (*m.events)[0].at <= now {
		event := m.events.pop()
		m.lastNow = max(m.lastNow, event.at)
		if event.spot < 0 || event.spot >= len(m.spots) {
			continue
		}
		spot := m.spots[event.spot]
		if spot.Generation != event.generation {
			continue
		}
		lease := m.leases[spot.Owner]
		switch event.kind {
		case eventShareStart, eventShareEnd:
			if lease != nil && leaseActive(lease, now) {
				if event.kind == eventShareStart {
				}
				m.refreshSpotAvailability(spot, now)
				m.scheduleShare(lease)
			}
		case eventLeaseStart, eventLeaseEnd:
			m.refreshSpotAvailability(spot, now)
			if event.kind == eventLeaseStart {
				m.scheduleShare(lease)
			}
		case eventGraceEnd:
			if lease != nil && now >= lease.GraceTo {
				m.releaseLease(spot, lease)
			}
		}
	}
}

func (m *Manager) releaseLease(spot *spotState, lease *Lease) {
	delete(m.leases, lease.Plate)
	spot.Owner = ""
	spot.Generation++
	if spot.Occupied == "" {
		spot.Public = true
		m.sharedFree.Remove(spot.ID)
		m.publicFree.Add(spot.ID)
	}
}

func (m *Manager) MonthlyEnter(now int, plate string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || strings.TrimSpace(plate) == "" {
		return 0, ErrInvalidArgument
	}
	if now < m.lastNow {
		return 0, ErrClockRolledBack
	}
	lease := m.leases[plate]
	if lease == nil {
		return 0, ErrNotFound
	}
	if m.vehicles[plate] != nil {
		return 0, ErrInvalidState
	}
	if !leaseActive(lease, now) {
		return 0, ErrLease
	}
	m.commit(now)
	home := m.spots[lease.Spot]
	if home.Occupied == "" {
		return m.parkVehicle(plate, home.ID, now, true, 0)
	}
	m.demandSpot(home, now)
	if id, ok := m.publicFree.Min(); ok {
		m.waiting = append(m.waiting, plate)
		return m.parkVehicle(plate, id, now, true, 0)
	}
	m.vehicles[plate] = &vehicleState{Plate: plate, MonthlyAtEntry: true, Entry: now, Spot: -1, Waiting: true, VacateStart: -1}
	m.waiting = append(m.waiting, plate)
	m.lastNow = now
	return -1, nil
}

func (m *Manager) VisitorEnter(now int, plate string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || strings.TrimSpace(plate) == "" {
		return 0, ErrInvalidArgument
	}
	if now < m.lastNow {
		return 0, ErrClockRolledBack
	}
	if m.vehicles[plate] != nil {
		return 0, ErrInvalidState
	}
	lease := m.leases[plate]
	if lease != nil && leaseActive(lease, now) {
		return 0, ErrLease
	}
	m.commit(now)
	if id, ok := m.publicFree.Min(); ok {
		return m.parkVehicle(plate, id, now, false, 0)
	}
	id, ok := m.sharedFree.Min()
	if !ok {
		return 0, ErrNoSpace
	}
	spot := m.spots[id]
	shareEnd := 0
	share := Interval{}
	hasShare := false
	if lease := m.leases[spot.Owner]; lease != nil {
		share, hasShare = lease.Share, lease.HasShare
		if _, end, open := intervalBoundsCovering(lease, now); open {
			shareEnd = end
		}
	}
	result, err := m.parkVehicle(plate, id, now, false, shareEnd)
	if err == nil {
		vehicle := m.vehicles[plate]
		vehicle.ShareAtEntry, vehicle.HasShareAtEntry = share, hasShare
		vehicle.EnteredShared = true
	}
	return result, err
}

func (m *Manager) parkVehicle(plate string, spotID, now int, monthly bool, shareEnd int) (int, error) {
	spot := m.spots[spotID]
	spot.Occupied = plate
	if spot.Public {
		m.publicFree.Remove(spotID)
	} else {
		m.sharedFree.Remove(spotID)
	}
	m.vehicles[plate] = &vehicleState{
		Plate: plate, MonthlyAtEntry: monthly, Entry: now, Spot: spotID, VacateStart: -1, ShareEndAtEntry: shareEnd,
	}
	m.lastNow = now
	return spotID, nil
}

func (m *Manager) Exit(now int, plate string) (ExitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < 0 || strings.TrimSpace(plate) == "" {
		return ExitResult{}, ErrInvalidArgument
	}
	if now < m.lastNow {
		return ExitResult{}, ErrClockRolledBack
	}
	vehicle := m.vehicles[plate]
	if vehicle == nil {
		return ExitResult{}, ErrNotFound
	}
	m.commit(now)
	result := ExitResult{}
	if vehicle.Waiting {
		result.Spot = -1
		delete(m.vehicles, plate)
		m.removeWaiting(plate)
		m.cancelOwnerDemand(plate, now)
		m.lastNow = now
		return result, nil
	}
	spot := m.spots[vehicle.Spot]
	result.Spot = spot.ID
	fee := m.exitFee(vehicle, now, spot)
	result.Fee, result.BaseFee, result.Overtime = fee.Total, fee.Base, fee.OverTime
	spot.Occupied = ""
	delete(m.vehicles, plate)
	m.removeWaiting(plate)
	m.cancelOwnerDemand(plate, now)
	if spot.Owner != "" {
		lease := m.leases[spot.Owner]
		if lease != nil && leaseActive(lease, now) && lease.HasShare && intervalOpen(lease, now) {
			m.sharedFree.Add(spot.ID)
		}
	} else {
		spot.Public = true
		m.publicFree.Add(spot.ID)
	}
	m.autoReturn(spot, now)
	m.lastNow = now
	return result, nil
}

func (m *Manager) demandSpot(home *spotState, now int) {
	if occupant := home.Occupied; occupant != "" {
		if visitor := m.vehicles[occupant]; visitor != nil && !visitor.MonthlyAtEntry {
			visitor.HasVacate = true
			visitor.VacateStart = now
			visitor.VacateDeadline = now + m.cfg.VacateMinutes
		}
	}
}

func (m *Manager) autoReturn(freed *spotState, now int) {
	for index, plate := range m.waiting {
		vehicle := m.vehicles[plate]
		lease := m.leases[plate]
		if vehicle == nil || lease == nil || !leaseActive(lease, now) || lease.Spot != freed.ID || !vehicle.Waiting {
			continue
		}
		m.waiting = append(m.waiting[:index], m.waiting[index+1:]...)
		freed.Occupied = plate
		freed.Public = false
		m.sharedFree.Remove(freed.ID)
		vehicle.Waiting = false
		vehicle.Spot = freed.ID
		return
	}
	for _, plate := range m.waiting {
		vehicle := m.vehicles[plate]
		if vehicle == nil || !vehicle.Waiting {
			continue
		}
		freed.Occupied = plate
		freed.Public = true
		m.publicFree.Remove(freed.ID)
		vehicle.Waiting = false
		vehicle.Spot = freed.ID
		return
	}
}

func (m *Manager) removeWaiting(plate string) {
	for index, waitingPlate := range m.waiting {
		if waitingPlate == plate {
			m.waiting = append(m.waiting[:index], m.waiting[index+1:]...)
			return
		}
	}
}

func (m *Manager) cancelOwnerDemand(plate string, now int) {
	lease := m.leases[plate]
	if lease == nil {
		return
	}
	spot := m.spots[lease.Spot]
	if occupant := spot.Occupied; occupant != "" {
		if visitor := m.vehicles[occupant]; visitor != nil && !visitor.MonthlyAtEntry && visitor.HasVacate {
			visitor.VacateEnd = now
		}
	}
}

func (m *Manager) exitFee(vehicle *vehicleState, now int, spot *spotState) feeBreakdown {
	if vehicle.MonthlyAtEntry {
		return feeBreakdown{}
	}
	shareEnds := [][2]int{}
	vacateEnd := vehicle.VacateEnd
	cancelledDemand := vehicle.VacateEnd > 0
	if !cancelledDemand && vehicle.HasVacate {
		vacateEnd = now
	}
	result := calculateFee(vehicle, now, now, shareEnds, vacateEnd, vehicle.ShareAtEntry, vehicle.HasShareAtEntry, m.cfg)
	return result
}
