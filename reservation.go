package parking

import "fmt"

type ReserveResult struct {
	ReservationID string
	SpotNumber    int
}

func validateBooking(zoneName, vehicle, id string, start, end, at int64) error {
	if zoneName == "" || vehicle == "" || id == "" || start < 0 || at < 0 || end <= start || start < at {
		return ErrInvalidArgument
	}
	return nil
}

func (l *Lot) Reserve(zoneName, vehicle, id string, start, end int64, needCharge bool, at int64) (ReserveResult, error) {
	if err := validateBooking(zoneName, vehicle, id, start, end, at); err != nil {
		return ReserveResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.begin(at, true); err != nil {
		return ReserveResult{}, err
	}
	z, ok := l.zones[zoneName]
	if !ok {
		return ReserveResult{}, ErrZoneNotFound
	}
	if _, exists := l.reservations[id]; exists {
		return ReserveResult{}, ErrInvalidArgument
	}
	number, ok := l.chooseSpot(z, start, end, needCharge)
	if !ok {
		return ReserveResult{}, ErrNoAvailableSpot
	}
	res := &Reservation{
		ID:         id,
		Zone:       zoneName,
		Vehicle:    vehicle,
		SpotNumber: number,
		Start:      start,
		End:        end,
		NeedCharge: needCharge,
		Status:     StatusReserved,
		CreatedAt:  at,
	}
	l.reservations[id] = res
	l.activeRes[id] = res
	l.assignOwner(zoneName, number, start, end, l.ownerOf(res))
	return ReserveResult{ReservationID: id, SpotNumber: number}, nil
}

func (l *Lot) RegisterWaitlist(zoneName, vehicle, id string, start, end int64, needCharge bool, at int64) error {
	if zoneName == "" || vehicle == "" || id == "" || start < 0 || at < 0 || end <= start || start < at {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.begin(at, true); err != nil {
		return err
	}
	z, ok := l.zones[zoneName]
	if !ok {
		return ErrZoneNotFound
	}
	if _, exists := l.reservations[id]; exists {
		return ErrInvalidArgument
	}
	l.joinWaitlist(z, waitEntry{
		id:         id,
		vehicle:    vehicle,
		start:      start,
		end:        end,
		needCharge: needCharge,
		createdAt:  at,
	})
	l.promoteAll(at)
	return nil
}

func (l *Lot) CheckIn(reservationID string, at int64) (int, error) {
	if reservationID == "" || at < 0 {
		return 0, ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.begin(at, false); err != nil {
		return 0, err
	}
	res, ok := l.reservations[reservationID]
	if !ok {
		return 0, ErrReservationGone
	}
	if res.Status == StatusCanceled || res.Status == StatusExpired || res.Status == StatusDeparted {
		return 0, ErrReservationDead
	}
	cfg := l.zones[res.Zone].config
	earliest := res.Start - cfg.EarlyWindow
	deadline := res.Start + cfg.GraceWindow
	if at < earliest {
		return 0, ErrEarlyArrival
	}
	if at > deadline {
		return 0, ErrReservationDead
	}
	if res.Status == StatusCheckedIn {
		return 0, ErrDuplicateCheckIn
	}
	if activeID, exists := l.active[res.Vehicle]; exists && activeID != res.ID {
		return 0, ErrVehicleOccupied
	}
	number := res.SpotNumber
	triggerAt := at
	if triggerAt < res.Start {
		triggerAt = res.Start
	}
	current := Owner{}
	holder := l.activeHolderOnSpot(res.Zone, number, res.ID)
	if holder != nil && holder.End <= triggerAt {
		current = l.ownerOf(holder)
	}
	if current.ReservationID != "" && current.ReservationID != res.ID {
		reassigned, found := l.reassignmentSpot(res.Zone, number, triggerAt, res.End, res.NeedCharge)
		if !found {
			return 0, ErrNoReassignment
		}
		offender := l.reservations[current.ReservationID]
		if offender != nil {
			offender.Penalty += cfg.OccupationPenalty
		}
		res.Award += cfg.OccupationPenalty
		res.Reassigned = true
		l.assignOwner(res.Zone, number, triggerAt, res.End, current)
		number = reassigned
		res.SpotNumber = number
		l.assignOwner(res.Zone, number, triggerAt, res.End, l.ownerOf(res))
	}
	res.Status = StatusCheckedIn
	res.CheckInAt = at
	l.active[res.Vehicle] = res.ID
	if at < triggerAt && l.rangeFree(res.Zone, number, at, triggerAt) {
		l.assignOwner(res.Zone, number, at, triggerAt, l.ownerOf(res))
	} else if at >= triggerAt {
		l.assignOwner(res.Zone, number, at, res.End, l.ownerOf(res))
	}
	return number, nil
}

func (l *Lot) activeHolderOnSpot(zoneName string, number int, selfID string) *Reservation {
	for _, holder := range l.activeRes {
		if holder.Zone == zoneName && holder.SpotNumber == number && holder.ID != selfID &&
			holder.Status == StatusCheckedIn {
			return holder
		}
	}
	return nil
}

func (l *Lot) reassignmentSpot(zoneName string, occupiedNumber int, from, to int64, needCharge bool) (int, bool) {
	z := l.zones[zoneName]
	kinds := []SpotKind{NormalSpot, ChargingSpot}
	if needCharge {
		kinds = []SpotKind{ChargingSpot}
	}
	for _, kind := range kinds {
		for _, number := range z.order {
			spot := z.spots[number]
			if spot.Kind == kind && number != occupiedNumber && l.rangeFree(zoneName, number, from, to) {
				return number, true
			}
		}
	}
	return 0, false
}

func (l *Lot) Cancel(reservationID string, at int64) error {
	if reservationID == "" || at < 0 {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.begin(at, true); err != nil {
		return err
	}
	res, ok := l.reservations[reservationID]
	if !ok {
		return ErrReservationGone
	}
	if res.Status != StatusReserved {
		return ErrReservationDead
	}
	cfg := l.zones[res.Zone].config
	if at >= res.Start {
		res.Penalty += cfg.NoShowFee
	}
	res.Status = StatusCanceled
	delete(l.activeRes, res.ID)
	l.assignOwner(res.Zone, res.SpotNumber, res.Start, res.End, Owner{})
	l.promoteAll(at)
	return nil
}

func (l *Lot) Depart(reservationID string, at int64) error {
	if reservationID == "" || at < 0 {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.begin(at, true); err != nil {
		return err
	}
	res, ok := l.reservations[reservationID]
	if !ok {
		return ErrReservationGone
	}
	if res.Status != StatusCheckedIn {
		return ErrReservationDead
	}
	if at < res.CheckInAt {
		return ErrClockRollback
	}
	res.Status = StatusDeparted
	res.DepartAt = at
	delete(l.active, res.Vehicle)
	delete(l.activeRes, res.ID)
	if at < res.End {
		l.assignOwner(res.Zone, res.SpotNumber, at, res.End, Owner{})
	}
	if at <= res.End {
		l.promoteAll(at)
	}
	return nil
}

func (l *Lot) SpotOwner(zoneName string, number int, at int64) (Owner, error) {
	if zoneName == "" || number <= 0 || at < 0 {
		return Owner{}, ErrInvalidArgument
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	z, ok := l.zones[zoneName]
	if !ok {
		return Owner{}, ErrZoneNotFound
	}
	if _, ok := z.spots[number]; !ok {
		return Owner{}, fmt.Errorf("车位不存在: %w", ErrInvalidArgument)
	}
	return l.ownerAt(zoneName, number, at), nil
}

func (l *Lot) Fee(reservationID string) (FeeDetail, error) {
	if reservationID == "" {
		return FeeDetail{}, ErrInvalidArgument
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	res, ok := l.reservations[reservationID]
	if !ok {
		return FeeDetail{}, ErrReservationGone
	}
	return feeFor(res, l.zones[res.Zone].config), nil
}

func (l *Lot) Reservation(reservationID string) (Reservation, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	res, ok := l.reservations[reservationID]
	if !ok {
		return Reservation{}, ErrReservationGone
	}
	return *res, nil
}
