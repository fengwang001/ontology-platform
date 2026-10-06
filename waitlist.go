package parking

import "sort"

func (s *Service) processExpirations(now Time) {
	s.processExpirationsStrict(now, true)
}

func (s *Service) processExpirationsStrict(now Time, expireAtDeadline bool) {
	for len(s.deadline) > 0 {
		r := s.deadline[0]
		if r.Status != StatusReserved {
			s.popDeadline()
			continue
		}
		deadline := s.graceDeadline(r)
		if deadline > now || (!expireAtDeadline && deadline == now) {
			return
		}
		s.popDeadline()
		if r.SpotID != "" {
			spot := s.spots[r.SpotID]
			for index, key := range r.IntervalKeys {
				if key >= deadline {
					spot.live.removeStart(key)
					continue
				}
				end := r.End
				if index+1 < len(r.IntervalKeys) {
					end = r.IntervalKeys[index+1]
				}
				if end > deadline {
					s.archiveInterval(spot, interval{start: key, end: deadline, reservationID: r.ID})
					spot.live.removeStart(key)
				}
			}
		}
		r.Status = StatusExpired
		r.ExpiredAt = deadline
		s.promoteFirst(r.ZoneID, r.Start, r.End)
	}
}

func (s *Service) pushDeadline(r *Reservation) {
	s.deadline = append(s.deadline, r)
	sort.Slice(s.deadline, func(i, j int) bool {
		left := s.graceDeadline(s.deadline[i])
		right := s.graceDeadline(s.deadline[j])
		if left != right {
			return left < right
		}
		if s.deadline[i].CreatedAt != s.deadline[j].CreatedAt {
			return s.deadline[i].CreatedAt < s.deadline[j].CreatedAt
		}
		return s.deadline[i].Vehicle < s.deadline[j].Vehicle
	})
}

func (s *Service) graceDeadline(r *Reservation) Time {
	base := r.Start
	if r.ConvertedAt > base {
		base = r.ConvertedAt
	}
	return base + Time(s.zones[r.ZoneID].GracePeriod)
}

func (s *Service) popDeadline() *Reservation {
	r := s.deadline[0]
	s.deadline = s.deadline[1:]
	return r
}

func (s *Service) removeDeadline(target *Reservation) {
	for i, r := range s.deadline {
		if r == target {
			s.deadline = append(s.deadline[:i], s.deadline[i+1:]...)
			return
		}
	}
}

func (s *Service) removeWaiter(target *Reservation) {
	queue := s.waiters[target.ZoneID]
	for i, r := range queue {
		if r == target {
			s.waiters[target.ZoneID] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

func (s *Service) promoteFirst(zoneID string, freeStart, freeEnd Time) {
	for index, waiter := range s.waiters[zoneID] {
		if waiter.Status != StatusWaiting {
			continue
		}
		if waiter.Start < freeStart || waiter.End > freeEnd {
			continue
		}
		spot := s.findAvailableSpot(zoneID, waiter.Start, waiter.End, waiter.NeedCharger)
		if spot == nil {
			continue
		}
		waiter.Status = StatusReserved
		waiter.ConvertedAt = s.now
		waiter.ConvertedFrom = waiter.ID
		zone := s.zones[zoneID]
		waiter.BaseFee = zone.RatePerSecond * Money(waiter.End-waiter.Start)
		s.insertReservationInterval(spot, waiter, waiter.Start, waiter.End)
		waiter.IntervalStart, waiter.IntervalEnd = waiter.Start, waiter.End
		waiter.IntervalKeys = []Time{waiter.Start}
		waiter.CurrentKey = waiter.Start
		s.waiters[zoneID] = append(s.waiters[zoneID][:index], s.waiters[zoneID][index+1:]...)
		s.pushDeadline(waiter)
		return
	}
}
