package parking

import "fmt"

func (m *Manager) RegisterMonthly(now int, plate string, start, end int) (Lease, error) {
	if err := validateLeaseDays(plate, start, end); err != nil {
		m.record("RegisterMonthly", now, plate, "invalid day range", err)
		return Lease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		m.recordLocked("RegisterMonthly", now, plate, "clock moved backward", err)
		return Lease{}, err
	}
	if reg := m.plates[plate]; reg != nil {
		err := ErrInvalidState
		m.recordLocked("RegisterMonthly", now, plate, "plate already registered", err)
		return Lease{}, err
	}
	if m.vehicles[plate] != nil {
		err := ErrInvalidState
		m.recordLocked("RegisterMonthly", now, plate, "vehicle is already in lot", err)
		return Lease{}, err
	}
	m.advance(now)
	spotID, ok := m.monthlyFree.min()
	if !ok {
		err := ErrNoSpace
		m.recordLocked("RegisterMonthly", now, plate, "no unregistered monthly spot", err)
		return Lease{}, err
	}
	m.monthlyFree.remove(spotID)
	reg := &registration{plate: plate, spot: spotID, start: start, end: end, version: 1}
	m.plates[plate] = reg
	m.registrations[spotID] = reg
	m.now = now
	m.pushTimer(timerLeaseEnd, end, plate, spotID, reg.version)
	m.pushTimer(timerGraceEnd, end+m.cfg.GraceDays*minutesPerDay, plate, spotID, reg.version)
	lease := Lease{SpotID: spotID, Plate: plate, Start: start, End: end}
	m.recordLocked("RegisterMonthly", now, plate, fmt.Sprintf("assigned spot %d", spotID), nil)
	return lease, nil
}

func (m *Manager) RenewMonthly(now int, plate string, end int) (Lease, error) {
	if plate == "" || !isDayBoundary(end) {
		err := ErrInvalidArgument
		m.record("RenewMonthly", now, plate, "invalid end day", err)
		return Lease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		m.recordLocked("RenewMonthly", now, plate, "clock moved backward", err)
		return Lease{}, err
	}
	reg := m.plates[plate]
	if reg == nil {
		err := ErrNotFound
		m.recordLocked("RenewMonthly", now, plate, "plate is not registered", err)
		return Lease{}, err
	}
	if end <= reg.end {
		err := ErrInvalidArgument
		m.recordLocked("RenewMonthly", now, plate, "new end must be later", err)
		return Lease{}, err
	}
	graceEnd := reg.end + m.cfg.GraceDays*minutesPerDay
	if now > graceEnd {
		err := ErrLease
		m.recordLocked("RenewMonthly", now, plate, "renewal grace period elapsed", err)
		return Lease{}, err
	}
	m.advance(now)
	reg = m.plates[plate]
	if reg == nil {
		err := ErrNotFound
		m.recordLocked("RenewMonthly", now, plate, "plate is not registered after time advance", err)
		return Lease{}, err
	}
	reg.end = end
	reg.version++
	m.now = now
	m.pushTimer(timerLeaseEnd, end, plate, reg.spot, reg.version)
	m.pushTimer(timerGraceEnd, end+m.cfg.GraceDays*minutesPerDay, plate, reg.spot, reg.version)
	lease := Lease{SpotID: reg.spot, Plate: plate, Start: reg.start, End: reg.end}
	m.recordLocked("RenewMonthly", now, plate, fmt.Sprintf("spot %d renewed until %d", reg.spot, end), nil)
	return lease, nil
}

func (m *Manager) SetShare(now int, ownerPlate string, interval Interval) error {
	if err := validateInterval(interval); err != nil {
		m.record("SetShare", now, ownerPlate, "invalid daily interval", err)
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkClock(now); err != nil {
		m.recordLocked("SetShare", now, ownerPlate, "clock moved backward", err)
		return err
	}
	reg := m.plates[ownerPlate]
	if reg == nil {
		err := ErrNotFound
		m.recordLocked("SetShare", now, ownerPlate, "owner plate is not registered", err)
		return err
	}
	if !leaseActive(reg, now) {
		err := ErrLease
		m.recordLocked("SetShare", now, ownerPlate, "monthly lease inactive", err)
		return err
	}
	m.advance(now)
	m.now = now
	reg = m.plates[ownerPlate]
	if reg == nil {
		err := ErrNotFound
		m.recordLocked("SetShare", now, ownerPlate, "owner registration vanished", err)
		return err
	}
	occupiedByOwner := m.occupants[reg.spot] != nil && m.occupants[reg.spot].plate == ownerPlate
	m.shares.add(reg.spot, interval, now, !occupiedByOwner)
	if visitor := m.occupants[reg.spot]; visitor != nil && !occupiedByOwner {
		m.resetShareObligation(visitor, now)
	}
	m.recordLocked("SetShare", now, ownerPlate, fmt.Sprintf("spot %d share updated", reg.spot), nil)
	return nil
}
