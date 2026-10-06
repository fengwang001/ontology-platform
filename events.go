package parking

import "container/heap"

func (m *Manager) advance(now int) {
	for m.timers.Len() > 0 && m.timers[0].at <= now {
		event := heap.Pop(&m.timers).(timerEvent)
		switch event.kind {
		case timerShareEnd:
			m.handleShareEnd(event, now)
		case timerEviction:
			m.handleEviction(event, now)
		case timerLeaseEnd:
			m.handleLeaseEnd(event)
		case timerGraceEnd:
			m.handleGraceEnd(event)
		}
	}
}

func (m *Manager) handleShareEnd(event timerEvent, now int) {
	v := m.vehicles[event.plate]
	if v == nil || v.monthly || v.public || v.spot != event.spotID ||
		v.shareVersion != event.version || v.evictOwner != "" {
		return
	}
	v.shareVersion = -1
	m.shares.setAvailable(v.spot, false)
	m.openOvertime(v, event.at)
}

func (m *Manager) handleEviction(event timerEvent, now int) {
	v := m.vehicles[event.plate]
	if v == nil || v.monthly || v.spot != event.spotID ||
		v.evictVersion != event.version || v.evictOwner == "" {
		return
	}
	m.openOvertime(v, event.at)
}

func (m *Manager) handleLeaseEnd(event timerEvent) {
	reg := m.registrations[event.spotID]
	if reg == nil || reg.plate != event.plate || reg.version != event.version ||
		reg.end != event.at {
		return
	}
	m.shares.remove(reg.spot)
	if occupant := m.occupants[reg.spot]; occupant == nil || occupant.plate != reg.plate {
		m.shares.setAvailable(reg.spot, false)
	}
	if occupant := m.occupants[reg.spot]; occupant != nil && !occupant.monthly && occupant.evictOwner == "" {
		occupant.shareVersion = -1
		m.openOvertime(occupant, event.at)
	}
}

func (m *Manager) handleGraceEnd(event timerEvent) {
	reg := m.registrations[event.spotID]
	if reg == nil || reg.plate != event.plate || reg.version != event.version ||
		reg.end+m.cfg.GraceDays*minutesPerDay != event.at {
		return
	}
	reg.retired = true
	if m.vehicles[reg.plate] == nil {
		delete(m.plates, reg.plate)
		delete(m.registrations, reg.spot)
		m.monthlyFree.add(reg.spot)
	}
}

func (m *Manager) resetShareObligation(v *vehicle, now int) {
	reg := m.registrations[v.spot]
	if reg == nil {
		return
	}
	entry, ok := m.shares.entries[v.spot]
	if !ok {
		return
	}
	m.closeOvertime(v, now)
	v.evictOwner = ""
	v.evictStart = 0
	end := nextShareEnd(v.entered, entry.interval)
	if now > v.entered {
		end = nextShareEnd(now, entry.interval)
	}
	v.shareVersion = entry.version
	m.pushTimer(timerShareEnd, end, v.plate, v.spot, entry.version)
}

func nextShareEnd(now int, interval Interval) int {
	dayStart := now / minutesPerDay * minutesPerDay
	if interval.StartMinute > interval.EndMinute {
		if now%minutesPerDay < interval.EndMinute {
			return dayStart + interval.EndMinute
		}
		return dayStart + minutesPerDay + interval.EndMinute
	}
	return dayStart + interval.EndMinute
}

func (m *Manager) openOvertime(v *vehicle, at int) {
	if !v.overtimeOpen {
		v.overtimeOpen = true
		v.overtime = append(v.overtime, intervalMinutes{start: at})
	}
}

func (m *Manager) closeOvertime(v *vehicle, now int) {
	if v.overtimeOpen {
		last := len(v.overtime) - 1
		v.overtime[last].end = now
		v.overtimeOpen = false
	}
}
