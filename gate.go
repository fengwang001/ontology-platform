package parking

import "fmt"

func (m *Manager) Enter(now int, plate string) (EntryResult, error) {
	if plate == "" {
		err := ErrInvalidArgument
		m.record("Enter", now, plate, "empty plate", err)
		return EntryResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		m.recordLocked("Enter", now, plate, "clock moved backward", err)
		return EntryResult{}, err
	}
	if m.vehicles[plate] != nil {
		err := ErrInvalidState
		m.recordLocked("Enter", now, plate, "vehicle is already in lot", err)
		return EntryResult{}, err
	}
	reg := m.plates[plate]
	if leaseActive(reg, now) {
		m.advance(now)
		m.now = now
		m.knownPlates[plate] = true
		return m.enterMonthly(now, reg)
	}
	return m.enterVisitor(now, plate)
}

func (m *Manager) Exit(now int, plate string) (ExitResult, error) {
	if plate == "" {
		err := ErrInvalidArgument
		m.record("Exit", now, plate, "empty plate", err)
		return ExitResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		m.recordLocked("Exit", now, plate, "clock moved backward", err)
		return ExitResult{}, err
	}
	if m.vehicles[plate] == nil {
		err := ErrNotFound
		reason := "plate has never been seen"
		if m.knownPlates[plate] {
			err = ErrInvalidState
			reason = "vehicle is not in lot"
		}
		m.recordLocked("Exit", now, plate, reason, err)
		return ExitResult{}, err
	}
	v := m.vehicles[plate]
	m.advance(now)
	m.now = now

	spotID := v.spot
	if v.monthly {
		m.exitMonthly(v, now)
		m.recordLocked("Exit", now, plate, "monthly vehicle left", nil)
		return ExitResult{SpotID: spotID}, nil
	}

	m.closeOvertime(v, now)
	v.freeEviction += m.remainingEviction(v, now)
	fee := calcFee(m.cfg, v.entered, now, v.freeEviction, v.overtime)
	m.releaseVisitorSpot(v, now)
	delete(m.vehicles, plate)
	m.recordLocked("Exit", now, plate, fmt.Sprintf("visitor left spot %d fee %d", spotID, fee), nil)
	return ExitResult{SpotID: spotID, Fee: fee}, nil
}

func (m *Manager) enterMonthly(now int, reg *registration) (EntryResult, error) {
	plate := reg.plate
	if m.occupants[reg.spot] == nil {
		v := &vehicle{plate: plate, spot: reg.spot, entered: now, monthly: true}
		m.vehicles[plate] = v
		m.occupants[reg.spot] = v
		m.shares.setAvailable(reg.spot, false)
		m.recordLocked("Enter", now, plate, fmt.Sprintf("monthly owner took own spot %d", reg.spot), nil)
		return EntryResult{SpotID: reg.spot}, nil
	}

	visitor := m.occupants[reg.spot]
	if visitor.monthly {
		err := ErrInvalidState
		m.recordLocked("Enter", now, plate, "monthly spot has an unexpected monthly occupant", err)
		return EntryResult{}, err
	}
	visitor.evictOwner = plate
	visitor.evictStart = now
	visitor.evictVersion++
	visitor.shareVersion = -1
	m.closeOvertime(visitor, now)
	m.pushTimer(timerEviction, now+m.cfg.EvictionTime, visitor.plate, reg.spot, visitor.evictVersion)

	owner := &vehicle{plate: plate, entered: now, monthly: true}
	m.vehicles[plate] = owner
	if publicID, ok := m.publicFree.min(); ok {
		m.publicFree.remove(publicID)
		owner.spot = publicID
		owner.public = true
		m.occupants[publicID] = owner
		m.recordLocked("Enter", now, plate, fmt.Sprintf("eviction started; owner uses public spot %d", publicID), nil)
		return EntryResult{SpotID: publicID, EvictionID: visitor.plate}, nil
	}
	owner.waiting = true
	m.waiting = append(m.waiting, plate)
	m.recordLocked("Enter", now, plate, "eviction started; owner waits", nil)
	return EntryResult{Waiting: true, EvictionID: visitor.plate}, nil
}

func (m *Manager) enterVisitor(now int, plate string) (EntryResult, error) {
	_, hasPublic := m.publicFree.min()
	_, hasShared := m.peekShared(now)
	if !hasPublic && !hasShared {
		err := ErrNoSpace
		m.recordLocked("Enter", now, plate, "no public or shared monthly spot", err)
		return EntryResult{}, err
	}
	m.advance(now)
	m.now = now
	m.knownPlates[plate] = true
	if actualPublic, ok := m.publicFree.min(); ok {
		m.publicFree.remove(actualPublic)
		m.placeVisitor(now, plate, actualPublic, true)
		m.recordLocked("Enter", now, plate, fmt.Sprintf("assigned public spot %d", actualPublic), nil)
		return EntryResult{SpotID: actualPublic}, nil
	}
	spotID, ok := m.shares.take(now)
	if !ok {
		err := ErrNoSpace
		m.recordLocked("Enter", now, plate, "no public or shared monthly spot", err)
		return EntryResult{}, err
	}
	m.placeVisitor(now, plate, spotID, false)
	m.recordLocked("Enter", now, plate, fmt.Sprintf("assigned shared monthly spot %d", spotID), nil)
	return EntryResult{SpotID: spotID}, nil
}

func (m *Manager) peekShared(now int) (int, bool) {
	return m.shares.peek(now)
}

func (m *Manager) placeVisitor(now int, plate string, spotID int, public bool) {
	v := &vehicle{plate: plate, spot: spotID, entered: now, public: public}
	m.vehicles[plate] = v
	m.occupants[spotID] = v
	if !public {
		m.resetShareObligation(v, now)
	}
}

func (m *Manager) remainingEviction(v *vehicle, now int) int {
	if v.evictOwner == "" {
		return 0
	}
	deadline := v.evictStart + m.cfg.EvictionTime
	if now <= deadline {
		return now - v.evictStart
	}
	return m.cfg.EvictionTime
}

func (m *Manager) releaseVisitorSpot(v *vehicle, now int) {
	spotID := v.spot
	delete(m.occupants, spotID)
	if v.public {
		m.releasePublic(spotID, now)
		return
	}
	if v.evictOwner != "" {
		owner := m.vehicles[v.evictOwner]
		if owner != nil && owner.monthly {
			reg := m.plates[owner.plate]
			if reg != nil && reg.spot == spotID {
				if owner.waiting {
					m.removeWaiting(owner.plate)
				} else if owner.public {
					m.releasePublic(owner.spot, now)
				}
				owner.waiting = false
				owner.public = false
				owner.spot = spotID
				m.occupants[spotID] = owner
				return
			}
		}
	}
	m.shares.release(spotID, now)
}

func (m *Manager) exitMonthly(v *vehicle, now int) {
	plate := v.plate
	reg := m.plates[plate]
	m.cancelEviction(v, now)
	if v.waiting {
		m.removeWaiting(plate)
		delete(m.vehicles, plate)
		return
	}
	delete(m.occupants, v.spot)
	delete(m.vehicles, plate)
	if v.public {
		m.releasePublic(v.spot, now)
		return
	}
	if reg != nil && leaseActive(reg, now) {
		m.shares.release(v.spot, now)
	}
	if reg != nil && reg.retired {
		m.monthlyFree.add(reg.spot)
	}
}

func (m *Manager) cancelEviction(owner *vehicle, now int) {
	if owner == nil || !owner.monthly {
		return
	}
	reg := m.plates[owner.plate]
	if reg == nil {
		return
	}
	if visitor := m.occupants[reg.spot]; visitor != nil && visitor.evictOwner == owner.plate {
		visitor.evictOwner = ""
		visitor.evictStart = 0
		visitor.evictVersion++
		if !visitor.overtimeOpen {
			m.resetShareObligation(visitor, now)
		}
	}
}

func (m *Manager) releasePublic(spotID int, now int) {
	if len(m.waiting) == 0 {
		m.publicFree.add(spotID)
		return
	}
	plate := m.waiting[0]
	m.waiting = m.waiting[1:]
	owner := m.vehicles[plate]
	if owner == nil || !owner.monthly {
		m.publicFree.add(spotID)
		return
	}
	owner.waiting = false
	owner.public = true
	owner.spot = spotID
	m.occupants[spotID] = owner
}

func (m *Manager) removeWaiting(plate string) {
	for i, item := range m.waiting {
		if item == plate {
			m.waiting = append(m.waiting[:i], m.waiting[i+1:]...)
			return
		}
	}
}
