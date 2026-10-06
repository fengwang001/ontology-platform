package pharmacy

import "sort"

func (s *System) addReservation(d *drug, ln *line, quantity, start int) {
	s.nextReserveID++
	res := &reservation{
		line:     ln,
		drugID:   d.id,
		quantity: quantity,
		start:    start,
		expireAt: start + s.r + 1,
	}
	d.reserved += quantity
	ln.reserved += quantity
	s.activeByLine[ln] = append(s.activeByLine[ln], res)
	s.pushEvent(res.expireAt, reserveExpiry{res: res})
}

func (s *System) pushEvent(at int, item heapItem) {
	s.nextEventOrder++
	event := &timedEvent{at: at, order: s.nextEventOrder, item: item}
	s.events = append(s.events, event)
	s.up(len(s.events) - 1)
}

func (s *System) up(index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if s.less(s.events[index], s.events[parent]) {
			s.events[index], s.events[parent] = s.events[parent], s.events[index]
			index = parent
			continue
		}
		return
	}
}

func (s *System) down(index int) {
	for {
		smallest := index
		left := index*2 + 1
		right := left + 1
		if left < len(s.events) && s.less(s.events[left], s.events[smallest]) {
			smallest = left
		}
		if right < len(s.events) && s.less(s.events[right], s.events[smallest]) {
			smallest = right
		}
		if smallest == index {
			return
		}
		s.events[index], s.events[smallest] = s.events[smallest], s.events[index]
		index = smallest
	}
}

func (s *System) less(a, b *timedEvent) bool {
	if a.at != b.at {
		return a.at < b.at
	}
	return a.order < b.order
}

func (s *System) popEvent() *timedEvent {
	event := s.events[0]
	last := len(s.events) - 1
	s.events[0] = s.events[last]
	s.events = s.events[:last]
	if len(s.events) > 0 {
		s.down(0)
	}
	return event
}

func (s *System) processThrough(now int) {
	for len(s.events) > 0 && s.events[0].at <= now {
		at := s.events[0].at
		batch := s.popEventsAt(at)
		released := make(map[string]*drug)
		expiredLines := make(map[lineAt]*drug)

		for _, event := range batch {
			item, ok := event.item.(prescriptionExpiry)
			if !ok || item.p.status != StatusActive {
				continue
			}
			item.p.status = StatusExpired
			for _, ln := range item.p.lines {
				d := s.drugs[ln.drugID]
				s.removeDebt(d, ln)
				if ln.reserved > 0 {
					s.releaseLineReservations(d, ln)
					released[d.id] = d
				}
			}
		}

		for _, event := range batch {
			item, ok := event.item.(reserveExpiry)
			if !ok || item.res.quantity <= 0 || item.res.expireAt != at {
				continue
			}
			d := s.drugs[item.res.drugID]
			s.releaseReservation(item.res, item.res.quantity)
			released[d.id] = d
			if item.res.line.p != nil && item.res.line.p.status == StatusActive {
				expiredLines[lineAt{line: item.res.line, at: at}] = d
			}
		}

		for item, d := range expiredLines {
			s.reenqueueExpiredLine(d, item.line, item.at)
		}

		ids := make([]string, 0, len(released))
		for id := range released {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			s.allocateDebts(released[id], at)
		}
	}
}

func (s *System) popEventsAt(at int) []*timedEvent {
	events := make([]*timedEvent, 0)
	for len(s.events) > 0 && s.events[0].at == at {
		events = append(events, s.popEvent())
	}
	return events
}

func (s *System) releaseLineReservations(d *drug, ln *line) {
	_ = d
	for len(s.activeByLine[ln]) > 0 {
		res := s.activeByLine[ln][0]
		if res.quantity > 0 {
			s.releaseReservation(res, res.quantity)
		}
	}
}

type lineAt struct {
	line *line
	at   int
}

func (s *System) reenqueueExpiredLine(d *drug, ln *line, at int) {
	if ln.p == nil || ln.p.status != StatusActive {
		return
	}
	remaining := ln.demand - ln.dispensed
	if remaining <= 0 || ln.reserved > 0 {
		return
	}
	increase := remaining - ln.owed
	if increase < 0 {
		increase = 0
	}
	ln.owed = remaining
	d.debt += increase
	if ln.queueElem == nil {
		s.insertDebtByAcceptOrder(d, ln.p, ln, at)
	}
}

func (s *System) releaseReservation(res *reservation, quantity int) {
	d := s.drugs[res.drugID]
	d.reserved -= quantity
	res.line.reserved -= quantity
	res.quantity = 0
	reservations := s.activeByLine[res.line]
	for i, active := range reservations {
		if active == res {
			last := len(reservations) - 1
			reservations[i] = reservations[last]
			s.activeByLine[res.line] = reservations[:last]
			break
		}
	}
	if len(s.activeByLine[res.line]) == 0 {
		delete(s.activeByLine, res.line)
	}
}
