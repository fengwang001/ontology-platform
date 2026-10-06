package ontology

import "container/heap"

type Permit struct {
	ID              string
	Household       string
	StartDay        int64
	EndDay          int64
	Noisy           bool
	Status          PermitStatus
	Extended        bool
	Complaints      int
	RevokedDay      int64
	LastComplainDay int64
	CheckedIn       bool
	heapVersion     int
}

type permit struct {
	Permit
}

func (s *Service) ApplyPermit(now int64, household string, startDay, endDay int64, noisy bool) (string, error) {
	if !nonEmptyID(household) {
		return "", illegal("household id is required")
	}
	if startDay < 0 || endDay <= startDay || endDay-startDay > s.cfg.MaxWorkDays {
		return "", illegal("construction interval must be positive and no longer than M days")
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
	today := now / MinutesPerDay
	if startDay <= today {
		return "", timeWindow("permit must be applied before the construction start day")
	}
	if s.availableBalance(household) < s.cfg.Penalty {
		return "", noMoney("deposit is insufficient to cover one penalty")
	}
	if current := s.currentPermit(household); current != nil {
		if isLivePermitAt(current.Status, today, current.EndDay) {
			return "", badState("household already has a valid permit")
		}
		if current.Status == PermitRevoked && startDay < current.RevokedDay+s.cfg.ReapplyWaitDays {
			return "", timeWindow("reapplication is earlier than W days after revocation")
		}
	}
	record := &permit{Permit: Permit{ID: s.newID("permit"), Household: household, StartDay: startDay, EndDay: endDay, Noisy: noisy, Status: PermitApplied}}
	s.permits[record.ID] = record
	s.householdPermit[household] = record
	s.addHold(household)
	record.heapVersion++
	heap.Push(&s.permitEnds, endItem{kind: endPermit, end: record.EndDay, id: record.ID, ver: record.heapVersion})
	return record.ID, nil
}

func (s *Service) ReviewPermit(now int64, permitID string, approve bool) error {
	if !nonEmptyID(permitID) {
		return illegal("permit id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return notFound("permit does not exist")
	}
	if today := now / MinutesPerDay; today >= record.EndDay {
		return timeWindow("permit has already expired")
	}
	if record.Status != PermitApplied {
		return badState("permit cannot be reviewed from its current state")
	}
	if approve {
		record.Status = PermitApproved
	} else {
		record.Status = PermitRejected
		s.releaseHold(record.Household)
	}
	return nil
}

func (s *Service) ExtendPermit(now int64, permitID string, newEndDay int64) error {
	if !nonEmptyID(permitID) {
		return illegal("permit id is required")
	}
	if newEndDay < 0 {
		return illegal("new end day must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return notFound("permit does not exist")
	}
	today := now / MinutesPerDay
	if record.Extended {
		return timeWindow("the one-time extension has already been used")
	}
	if today >= record.EndDay {
		return timeWindow("extension must be requested before expiry")
	}
	if record.Status != PermitApplied && record.Status != PermitApproved && record.Status != PermitActive && record.Status != PermitSuspended {
		return badState("permit cannot be extended from its current state")
	}
	if newEndDay <= record.EndDay || newEndDay-record.StartDay > s.cfg.MaxWorkDays {
		return illegal("new interval must extend the permit and remain within M days")
	}
	record.EndDay = newEndDay
	record.Extended = true
	record.heapVersion++
	heap.Push(&s.permitEnds, endItem{kind: endPermit, end: record.EndDay, id: record.ID, ver: record.heapVersion})
	return nil
}

func (s *Service) PermitCheckIn(now int64, permitID string) error {
	if !nonEmptyID(permitID) {
		return illegal("permit id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return notFound("permit does not exist")
	}
	today := now / MinutesPerDay
	if today < record.StartDay || today >= record.EndDay {
		return timeWindow("check-in is outside the approved construction interval")
	}
	if record.Status == PermitSuspended {
		return badState("construction is suspended")
	}
	if record.Status == PermitRevoked {
		return badState("permit has been revoked")
	}
	if record.Status != PermitApproved && record.Status != PermitActive {
		return badState("permit is not approved for construction")
	}
	if record.Noisy && s.isQuiet(now) {
		return quiet("noisy construction cannot start in a quiet period")
	}
	record.Status = PermitActive
	record.CheckedIn = true
	return nil
}

func (s *Service) PermitCheckOut(now int64, permitID string) error {
	if !nonEmptyID(permitID) {
		return illegal("permit id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return notFound("permit does not exist")
	}
	if record.Status != PermitActive && record.Status != PermitSuspended {
		return badState("permit construction is not checked in")
	}
	if !record.CheckedIn {
		return badState("permit construction is not checked in")
	}
	record.CheckedIn = false
	if record.Status == PermitActive {
		record.Status = PermitApproved
	}
	return nil
}

func (s *Service) AddComplaint(now int64, permitID string) error {
	if !nonEmptyID(permitID) {
		return illegal("permit id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return notFound("permit does not exist")
	}
	today := now / MinutesPerDay
	effectiveStatus := record.Status
	if today >= record.EndDay &&
		(effectiveStatus == PermitApplied || effectiveStatus == PermitApproved || effectiveStatus == PermitSuspended) {
		effectiveStatus = PermitExpired
	}
	if effectiveStatus == PermitRejected || effectiveStatus == PermitRevoked || effectiveStatus == PermitRefunded {
		return badState("permit cannot receive a complaint")
	}
	if effectiveStatus == PermitExpired && record.Status == PermitApplied {
		return badState("an unstarted application cannot receive a complaint")
	}
	record.Status = effectiveStatus
	record.Complaints++
	record.LastComplainDay = today
	if record.Complaints >= s.cfg.ComplaintLimit {
		record.Status = PermitRevoked
		record.RevokedDay = today
		record.CheckedIn = false
		s.releaseHold(record.Household)
	} else if isLivePermitAt(record.Status, today, record.EndDay) {
		record.Status = PermitSuspended
	}
	return nil
}

func (s *Service) LiftSuspension(now int64, permitID string) error {
	if !nonEmptyID(permitID) {
		return illegal("permit id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return notFound("permit does not exist")
	}
	if record.Status != PermitSuspended {
		return badState("permit is not suspended")
	}
	if now/MinutesPerDay >= record.EndDay {
		return badState("permit expired while suspended")
	}
	if record.CheckedIn {
		record.Status = PermitActive
	} else {
		record.Status = PermitApproved
	}
	return nil
}

func (s *Service) RefundPermit(now int64, permitID string) error {
	if !nonEmptyID(permitID) {
		return illegal("permit id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return notFound("permit does not exist")
	}
	today := now / MinutesPerDay
	effectiveStatus := record.Status
	if today >= record.EndDay && (effectiveStatus == PermitApproved || effectiveStatus == PermitActive) {
		effectiveStatus = PermitExpired
	}
	if effectiveStatus != PermitExpired && effectiveStatus != PermitActive {
		return badState("permit is not eligible for refund")
	}
	if today < record.EndDay {
		return badState("construction interval has not terminated")
	}
	latest := record.EndDay
	if record.LastComplainDay > latest {
		latest = record.LastComplainDay
	}
	if today < latest+s.cfg.RefundDays {
		return timeWindow("refund waiting period has not elapsed")
	}
	record.Status = PermitRefunded
	record.CheckedIn = false
	s.releaseHold(record.Household)
	s.refund(record.Household, record.ID, now, s.cfg.Penalty)
	return nil
}

func (s *Service) GetPermit(now int64, permitID string) (*Permit, error) {
	if !nonEmptyID(permitID) {
		return nil, illegal("permit id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	s.advanceClock(now)
	record := s.permits[permitID]
	if record == nil {
		return nil, notFound("permit does not exist")
	}
	copyValue := record.Permit
	return &copyValue, nil
}

func (s *Service) currentPermit(household string) *permit { return s.householdPermit[household] }

func isLivePermit(status PermitStatus) bool {
	return status == PermitApplied || status == PermitApproved || status == PermitActive || status == PermitSuspended
}

func isLivePermitAt(status PermitStatus, today, endDay int64) bool {
	return (status == PermitApplied || status == PermitApproved ||
		status == PermitActive || status == PermitSuspended) && today < endDay
}
