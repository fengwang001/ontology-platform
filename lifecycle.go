package parking

import "fmt"

func (s *Service) Reserve(now Time, req ReservationRequest) (ReserveResult, error) {
	if req.Vehicle == "" || req.End <= req.Start {
		return ReserveResult{}, Error{Code: ErrInvalidArgument, Msg: "invalid reservation request"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return ReserveResult{}, err
	}
	if req.ReservationID != "" {
		if _, exists := s.res[req.ReservationID]; exists {
			return ReserveResult{}, Error{Code: ErrInvalidArgument, Msg: "duplicate reservation id"}
		}
	}
	zone, ok := s.zones[req.ZoneID]
	if !ok {
		return ReserveResult{}, Error{Code: ErrZoneNotFound, Msg: req.ZoneID}
	}
	if now > req.Start {
		return ReserveResult{}, Error{Code: ErrInvalidArgument, Msg: "reservation starts in the past"}
	}
	if err := s.snapshotNow(now); err != nil {
		return ReserveResult{}, err
	}
	s.processExpirations(now)
	spot := s.findAvailableSpot(req.ZoneID, req.Start, req.End, req.NeedCharger)
	if spot == nil {
		return ReserveResult{}, Error{Code: ErrNoAvailableSpot, Msg: "no matching spot"}
	}
	r := s.makeReservation(req, now)
	s.insertReservationInterval(spot, r, req.Start, req.End)
	r.IntervalStart, r.IntervalEnd = req.Start, req.End
	r.IntervalKeys = []Time{req.Start}
	r.CurrentKey = req.Start
	r.BaseFee = zone.RatePerSecond * Money(req.End-req.Start)
	s.res[r.ID] = r
	s.pushDeadline(r)
	return ReserveResult{ReservationID: r.ID, Status: r.Status, SpotID: r.SpotID, Reason: "accepted"}, nil
}

func (s *Service) RegisterWait(now Time, req ReservationRequest) (WaitResult, error) {
	if req.Vehicle == "" || req.End <= req.Start {
		return WaitResult{}, Error{Code: ErrInvalidArgument, Msg: "invalid wait request"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return WaitResult{}, err
	}
	if req.ReservationID != "" {
		if _, exists := s.res[req.ReservationID]; exists {
			return WaitResult{}, Error{Code: ErrInvalidArgument, Msg: "duplicate reservation id"}
		}
	}
	if _, ok := s.zones[req.ZoneID]; !ok {
		return WaitResult{}, Error{Code: ErrZoneNotFound, Msg: req.ZoneID}
	}
	if now > req.Start {
		return WaitResult{}, Error{Code: ErrInvalidArgument, Msg: "wait starts in the past"}
	}
	if err := s.snapshotNow(now); err != nil {
		return WaitResult{}, err
	}
	s.processExpirations(now)
	if s.findAvailableSpot(req.ZoneID, req.Start, req.End, req.NeedCharger) != nil {
		return WaitResult{}, Error{Code: ErrInvalidArgument, Msg: "a spot is currently available; reserve directly"}
	}
	r := s.makeReservation(req, now)
	r.Status = StatusWaiting
	s.waiters[req.ZoneID] = append(s.waiters[req.ZoneID], r)
	s.res[r.ID] = r
	return WaitResult{ReservationID: r.ID, Position: len(s.waiters[req.ZoneID]), Reason: "queued"}, nil
}

func (s *Service) CancelWait(now Time, reservationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	r, ok := s.res[reservationID]
	if !ok {
		return Error{Code: ErrReservationGone, Msg: reservationID}
	}
	if r.Status != StatusWaiting {
		return Error{Code: ErrReservationStale, Msg: "not waiting"}
	}
	if err := s.snapshotNow(now); err != nil {
		return err
	}
	s.removeWaiter(r)
	delete(s.res, r.ID)
	return nil
}

func (s *Service) CheckIn(now Time, reservationID string) (CheckInResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return CheckInResult{}, err
	}
	r, ok := s.res[reservationID]
	if !ok {
		return CheckInResult{}, Error{Code: ErrReservationGone, Msg: reservationID}
	}
	if r.Status == StatusCancelled || r.Status == StatusExpired || r.Status == StatusCompleted {
		return CheckInResult{}, Error{Code: ErrReservationStale, Msg: string(r.Status)}
	}
	if r.Status == StatusOccupied {
		return CheckInResult{}, Error{Code: ErrDuplicateCheckIn, Msg: reservationID}
	}
	if s.hasOtherActiveVehicle(r.Vehicle, r.ID) {
		return CheckInResult{}, Error{Code: ErrVehicleCheckedIn, Msg: r.Vehicle}
	}
	zone := s.zones[r.ZoneID]
	windowStart := r.Start - Time(zone.EarlyWindow)
	if r.ConvertedAt > 0 {
		if convertedStart := r.ConvertedAt; convertedStart > windowStart {
			windowStart = convertedStart
		}
	}
	windowEnd := s.graceDeadline(r)
	if now < windowStart {
		return CheckInResult{}, Error{Code: ErrEarlyArrival, Msg: fmt.Sprintf("window starts at %d", windowStart)}
	}
	if now > windowEnd {
		return CheckInResult{}, Error{Code: ErrReservationStale, Msg: "grace period ended"}
	}
	if err := s.snapshotNow(now); err != nil {
		return CheckInResult{}, err
	}
	s.processExpirationsStrict(now, false)
	if r.Status != StatusReserved {
		return CheckInResult{}, Error{Code: ErrReservationStale, Msg: string(r.Status)}
	}

	oldSpot := s.spots[r.SpotID]
	needsMove := r.PendingMove
	if oldSpot != nil && !needsMove {
		existing := oldSpot.live.findOverlappingOther(now, maxTime(now+1, r.End), r.ID)
		if existing != nil && existing.reservationID != r.ID {
			needsMove = true
		}
	}
	if needsMove {
		newSpot := s.findAvailableSpotExcluding(r.ZoneID, now, r.End, r.NeedCharger, r.SpotID)
		if newSpot == nil {
			return CheckInResult{}, Error{Code: ErrNoReassignment, Msg: "no matching free spot"}
		}
		oldID := r.SpotID
		if oldSpot != nil {
			s.archiveInterval(oldSpot, interval{start: r.IntervalStart, end: now, reservationID: r.ID})
			oldSpot.live.removeStart(r.CurrentKey)
		}
		newSpot.live.addRaw(interval{start: now, end: r.End, reservationID: r.ID})
		r.SpotID = newSpot.definition.ID
		r.Kind = newSpot.definition.Kind
		r.AssignedAt = s.now
		r.IntervalStart = now
		r.CurrentKey = now
		r.IntervalEnd = r.End
		r.IntervalKeys = []Time{now}
		r.Reassigned = true
		r.PendingMove = false
		if r.OriginalSpotID == "" {
			r.OriginalSpotID = oldID
		}
		s.promoteFirst(r.ZoneID, now, r.End)
	} else if now < r.Start {
		if oldSpot != nil {
			oldSpot.live.addRaw(interval{start: now, end: r.Start, reservationID: r.ID})
			oldSpot.live.setInterval(r.CurrentKey, interval{start: r.Start, end: r.End, reservationID: r.ID})
			r.IntervalKeys = append(r.IntervalKeys, now)
		}
		r.IntervalStart = now
		r.CurrentKey = now
	}
	r.Status = StatusOccupied
	r.CheckedInAt = now
	return CheckInResult{ReservationID: r.ID, SpotID: r.SpotID, Reason: "checked in"}, nil
}

func (s *Service) Cancel(now Time, reservationID string) (CancelResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return CancelResult{}, err
	}
	r, ok := s.res[reservationID]
	if !ok {
		return CancelResult{}, Error{Code: ErrReservationGone, Msg: reservationID}
	}
	if r.Status == StatusCancelled || r.Status == StatusExpired || r.Status == StatusCompleted {
		return CancelResult{}, Error{Code: ErrReservationStale, Msg: string(r.Status)}
	}
	if r.Status == StatusOccupied {
		return CancelResult{}, Error{Code: ErrReservationStale, Msg: "checked in reservations must leave"}
	}
	if err := s.snapshotNow(now); err != nil {
		return CancelResult{}, err
	}
	s.processExpirations(now)
	if r.Status == StatusCancelled || r.Status == StatusExpired || r.Status == StatusCompleted {
		return CancelResult{}, Error{Code: ErrReservationStale, Msg: string(r.Status)}
	}
	if r.Status == StatusWaiting {
		s.removeWaiter(r)
		r.Status = StatusCancelled
		r.CancelledAt = now
		return CancelResult{ReservationID: r.ID, Reason: "waiting cancelled"}, nil
	}
	zone := s.zones[r.ZoneID]
	if now >= r.Start {
		r.NoShowFee = zone.NoShowFee
	}
	if r.SpotID != "" {
		spot := s.spots[r.SpotID]
		if now <= r.IntervalStart {
			for _, key := range r.IntervalKeys {
				spot.live.removeStart(key)
			}
		} else if now < r.IntervalEnd {
			archiveStart := r.IntervalStart
			if r.CheckedInAt > 0 {
				archiveStart = maxTime(r.IntervalStart, r.CheckedInAt)
			}
			if archiveStart < now {
				s.archiveInterval(spot, interval{start: archiveStart, end: now, reservationID: r.ID})
			}
			for _, key := range r.IntervalKeys {
				spot.live.removeStart(key)
			}
		}
	}
	s.removeDeadline(r)
	r.Status = StatusCancelled
	r.CancelledAt = now
	s.promoteFirst(r.ZoneID, r.Start, r.End)
	return CancelResult{ReservationID: r.ID, NoShowFee: r.NoShowFee, Reason: "cancelled"}, nil
}

func (s *Service) Leave(now Time, reservationID string) (LeaveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return LeaveResult{}, err
	}
	r, ok := s.res[reservationID]
	if !ok {
		return LeaveResult{}, Error{Code: ErrReservationGone, Msg: reservationID}
	}
	if r.Status != StatusOccupied {
		return LeaveResult{}, Error{Code: ErrReservationStale, Msg: "reservation is not occupied"}
	}
	if now < r.CheckedInAt {
		return LeaveResult{}, Error{Code: ErrClockRolledBack, Msg: "leave before check-in"}
	}
	if err := s.snapshotNow(now); err != nil {
		return LeaveResult{}, err
	}
	zone := s.zones[r.ZoneID]
	spot := s.spots[r.SpotID]
	var overtimeFee, compensation Money
	if now > r.End {
		minutes := (int64(now-r.End) + 59) / 60
		overtimeFee = Money(minutes) * zone.OvertimeRatePerMinute
		blocked := spot.live.allOverlappingFrom(r.End, now, r.ID)
		for _, blocker := range blocked {
			next := s.res[blocker.reservationID]
			if next == nil || next.Status != StatusReserved {
				continue
			}
			destination := s.findAvailableSpotExcluding(r.ZoneID, now, next.End, next.NeedCharger, spot.definition.ID)
			s.archiveInterval(spot, interval{start: next.IntervalStart, end: now, reservationID: next.ID})
			spot.live.removeStart(next.CurrentKey)
			if destination != nil {
				destination.live.addRaw(interval{start: now, end: next.End, reservationID: next.ID})
				next.SpotID = destination.definition.ID
				next.Kind = destination.definition.Kind
				next.AssignedAt = s.now
				next.IntervalStart, next.IntervalEnd = now, next.End
				next.IntervalKeys = []Time{now}
				next.CurrentKey = now
				next.Reassigned = true
				next.OriginalSpotID = spot.definition.ID
				s.promoteFirst(r.ZoneID, now, next.End)
			} else {
				next.SpotID = ""
				next.Kind = ""
				next.PendingMove = true
				next.OriginalSpotID = spot.definition.ID
			}
			compensation = zone.ReassignmentCompensation
		}
		archiveStart := r.IntervalStart
		if r.CheckedInAt > 0 {
			archiveStart = maxTime(r.IntervalStart, r.CheckedInAt)
		}
		if archiveStart < now {
			s.archiveInterval(spot, interval{start: archiveStart, end: now, reservationID: r.ID})
		}
		for _, key := range r.IntervalKeys {
			spot.live.removeStart(key)
		}
		r.IntervalEnd = now
	} else if now < r.IntervalEnd {
		archiveStart := r.IntervalStart
		if r.CheckedInAt > 0 {
			archiveStart = maxTime(r.IntervalStart, r.CheckedInAt)
		}
		if archiveStart < now {
			s.archiveInterval(spot, interval{start: archiveStart, end: now, reservationID: r.ID})
		}
		for _, key := range r.IntervalKeys {
			spot.live.removeStart(key)
		}
		r.IntervalEnd = now
	}
	r.OvertimeFee = overtimeFee
	r.Compensation += compensation
	r.Status = StatusCompleted
	r.LeftAt = now
	total := r.BaseFee + r.OvertimeFee + r.NoShowFee + r.Compensation
	return LeaveResult{ReservationID: r.ID, BaseFee: r.BaseFee, OvertimeFee: r.OvertimeFee, Compensation: r.Compensation, Total: total, Reason: "left"}, nil
}

func (s *Service) makeReservation(req ReservationRequest, now Time) *Reservation {
	id := req.ReservationID
	if id == "" {
		id = s.nextID("R")
	}
	return &Reservation{
		ID:            id,
		ZoneID:        req.ZoneID,
		Vehicle:       req.Vehicle,
		Start:         req.Start,
		End:           req.End,
		BillingStart:  req.Start,
		NeedCharger:   req.NeedCharger,
		Status:        StatusReserved,
		CreatedAt:     now,
		IntervalStart: req.Start,
		IntervalEnd:   req.End,
	}
}

func (s *Service) hasOtherActiveVehicle(vehicle, reservationID string) bool {
	for _, r := range s.res {
		if r.Vehicle == vehicle && r.ID != reservationID && r.Status == StatusOccupied {
			return true
		}
	}
	return false
}

func maxTime(a, b Time) Time {
	if a > b {
		return a
	}
	return b
}
func (s *Service) findAvailableSpot(zoneID string, start, end Time, needCharger bool) *spotState {
	return s.findAvailableSpotExcluding(zoneID, start, end, needCharger, "")
}

func (s *Service) findAvailableSpotExcluding(zoneID string, start, end Time, needCharger bool, excludedSpot string) *spotState {
	kinds := []SpotKind{SpotNormal, SpotCharger}
	if needCharger {
		kinds = []SpotKind{SpotCharger}
	}
	for _, kind := range kinds {
		for _, spot := range s.zonesSpots[zoneID] {
			if spot.definition.Kind != kind {
				continue
			}
			if spot.definition.ID == excludedSpot {
				continue
			}
			if spot.live.findOverlapping(start, end) == nil {
				return spot
			}
		}
	}
	return nil
}

func (s *Service) insertReservationInterval(spot *spotState, reservation *Reservation, start, end Time) {
	err := spot.live.add(interval{
		start:         start,
		end:           end,
		reservationID: reservation.ID,
	})
	if err != nil {
		panic(err)
	}
	reservation.SpotID = spot.definition.ID
	reservation.Kind = spot.definition.Kind
	reservation.AssignedAt = s.now
}

func (s *Service) removeReservationInterval(reservation *Reservation) {
	if reservation.SpotID == "" {
		return
	}
	spot := s.spots[reservation.SpotID]
	if spot != nil {
		spot.live.removeStart(reservation.IntervalStart)
	}
}

func (s *Service) replaceReservationInterval(spot *spotState, reservation *Reservation, start, end Time) {
	s.removeReservationInterval(reservation)
	s.insertReservationInterval(spot, reservation, start, end)
}
