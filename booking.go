package ontology

import "container/heap"

type Booking struct {
	ID        string
	Household string
	Elevator  string
	Start     int64
	End       int64
	Kind      MoveKind
	Status    BookingStatus
	Penalties int
}

type booking struct {
	Booking
	dayKey int64
}

type slotKey struct {
	elevator string
	start    int64
}

type householdDay struct {
	household string
	day       int64
}

func (s *Service) BookMove(now int64, household, elevator string, start, end int64, kind MoveKind) (string, error) {
	if !nonEmptyID(household) || !nonEmptyID(elevator) || (kind != MoveIn && kind != MoveOut) {
		return "", illegal("household, elevator and a valid move kind are required")
	}
	if start < 0 || end <= start {
		return "", illegal("booking interval must be a non-empty integer-minute range")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return "", err
	}
	s.advanceClock(now)
	if _, ok := s.households[household]; !ok {
		return "", notFound("household does not exist")
	}
	if _, ok := s.elevators[elevator]; !ok {
		return "", notFound("elevator does not exist")
	}
	advance := start - now
	if advance < s.cfg.AdvanceMin || advance > s.cfg.MaxAdvanceMin {
		return "", timeWindow("booking lead time is outside [L,U]")
	}
	if s.availableBalance(household) < s.cfg.Penalty {
		return "", noMoney("deposit is insufficient to cover one penalty")
	}
	dayKey := householdDay{household, start / MinutesPerDay}
	if dayRecord := s.dayBookings[dayKey]; dayRecord != nil {
		if dayRecord.Status == BookingReserved || dayRecord.Status == BookingActive {
			return "", badState("household already has an active booking on that day")
		}
	}
	for slotStart := start; slotStart < end; slotStart++ {
		if owner := s.slots[slotKey{elevator, slotStart}]; owner != nil &&
			(owner.Status == BookingReserved || owner.Status == BookingActive) {
			return "", badState("an elevator slot is already occupied")
		}
	}
	record := &booking{
		Booking: Booking{ID: s.newID("move"), Household: household, Elevator: elevator, Start: start, End: end, Kind: kind, Status: BookingReserved},
		dayKey:  start / MinutesPerDay,
	}
	s.bookings[record.ID] = record
	for slotStart := start; slotStart < end; slotStart++ {
		s.slots[slotKey{elevator, slotStart}] = record
	}
	s.dayBookings[dayKey] = record
	s.addHold(household)
	heap.Push(&s.bookingEnds, endItem{kind: endBooking, end: record.End, id: record.ID})
	return record.ID, nil
}

func (s *Service) CancelBooking(now int64, bookingID string) error {
	if !nonEmptyID(bookingID) {
		return illegal("booking id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.bookings[bookingID]
	if record == nil {
		return notFound("booking does not exist")
	}
	if now >= record.Start {
		return timeWindow("cancellation is no longer allowed after slot start")
	}
	if record.Status != BookingReserved {
		return badState("booking cannot be cancelled from its current state")
	}
	record.Status = BookingCanceled
	if record.Start-now < s.cfg.AdvanceMin {
		s.chargePenalty(record.Household, record.ID, now)
		record.Penalties++
	} else {
		s.releaseHold(record.Household)
	}
	return nil
}

func (s *Service) BookingCheckIn(now int64, bookingID string) error {
	if !nonEmptyID(bookingID) {
		return illegal("booking id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.bookings[bookingID]
	if record == nil {
		return notFound("booking does not exist")
	}
	if now < record.Start-s.cfg.CheckInLeadMin || now > record.End {
		return timeWindow("check-in is outside its allowed window")
	}
	if record.Status != BookingReserved {
		return badState("booking cannot check in from its current state")
	}
	if owner := s.elevatorActive[record.Elevator]; owner != nil && owner != record {
		return badState("previous elevator use has not checked out")
	}
	record.Status = BookingActive
	s.elevatorActive[record.Elevator] = record
	return nil
}

func (s *Service) BookingCheckOut(now int64, bookingID string) error {
	if !nonEmptyID(bookingID) {
		return illegal("booking id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.bookings[bookingID]
	if record == nil {
		return notFound("booking does not exist")
	}
	if record.Status != BookingActive {
		return badState("booking is not active")
	}
	record.Status = BookingDone
	s.releaseHold(record.Household)
	if s.elevatorActive[record.Elevator] == record {
		delete(s.elevatorActive, record.Elevator)
	}
	return nil
}

func (s *Service) GetBooking(now int64, bookingID string) (*Booking, error) {
	if !nonEmptyID(bookingID) {
		return nil, illegal("booking id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.advanceClock(now)
	record := s.bookings[bookingID]
	if record == nil {
		return nil, notFound("booking does not exist")
	}
	copyValue := record.Booking
	return &copyValue, nil
}
