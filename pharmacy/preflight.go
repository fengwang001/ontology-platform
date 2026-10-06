package pharmacy

import "sort"

func (s *System) canDispenseAfterEvents(p *prescription, now int) bool {
	if s.totalReserved(p) > 0 && s.reservationActiveUntil(p, now) {
		return true
	}
	for _, ln := range p.lines {
		if s.lineReceivesFromEvents(ln, now) {
			return true
		}
	}
	return false
}

func (s *System) reservationActiveUntil(p *prescription, now int) bool {
	for _, ln := range p.lines {
		for _, res := range s.activeByLine[ln] {
			if res.quantity > 0 && res.expireAt > now {
				return true
			}
		}
	}
	return false
}

func (s *System) lineReceivesFromEvents(target *line, now int) bool {
	d := s.drugs[target.drugID]
	type release struct{ at, quantity int }
	var releases []release
	prescriptionExpires := map[*prescription]bool{}
	for _, event := range s.events {
		if event.at > now {
			continue
		}
		switch item := event.item.(type) {
		case reserveExpiry:
			if item.res.quantity > 0 && item.res.drugID == d.id {
				releases = append(releases, release{item.res.expireAt, item.res.quantity})
			}
		case prescriptionExpiry:
			if item.p.status == StatusActive {
				prescriptionExpires[item.p] = true
			}
		}
	}
	if prescriptionExpires[target.p] {
		return false
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].at < releases[j].at })
	additional := 0
	for _, item := range releases {
		additional += item.quantity
		if s.simulateAllocationAt(d, target, item.at, additional, prescriptionExpires) {
			return true
		}
	}
	return false
}

func (s *System) simulateAllocationAt(d *drug, target *line, at, additional int, prescriptionExpires map[*prescription]bool) bool {
	available := d.available() + additional
	element := d.queue.Front()
	for element != nil {
		entry := element.Value.(*debtEntry)
		active := entry.p.status == StatusActive && !prescriptionExpires[entry.p]
		if !active || entry.ln.owed <= 0 {
			element = element.Next()
			continue
		}
		if entry.ln == target {
			return reservationQuantity(d, target.owed, available) > 0
		}
		quantity := reservationQuantity(d, entry.ln.owed, available)
		if quantity > 0 {
			available -= quantity
		}
		if available <= 0 || (!d.splittable && quantity == 0) {
			return false
		}
		element = element.Next()
	}
	return false
}
