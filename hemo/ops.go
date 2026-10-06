package hemo

import "sort"

// removeLive deletes a treatment from all live indexes (the registry entry
// is retained by the caller or deleted with it).
func (s *System) removeLive(t *Treatment) {
	s.chairTimelines[t.ChairID].Remove(t)
	s.patientTimelines[t.PatientID].Remove(t)
	delete(s.treats, t.ID)
}

func (s *System) putLive(t *Treatment) {
	s.treats[t.ID] = t
	s.chairTimelines[t.ChairID].Add(t)
	s.patientTimelines[t.PatientID].Add(t)
}

// reassignChair removes affected live treatments (they must all currently
// sit on chairID) and re-admits them atomically, sorted by start. Locked
// items keep their current chair when feasible; for fault reassignment the
// caller sets the chair's fault window first, so feasibility naturally
// forces a move away from the faulty chair.
//
// On any failure all removed treatments are restored exactly as before and
// the returned error explains the first (highest priority) failure.
func (s *System) reassignChair(affected []*Treatment) error {
	sort.SliceStable(affected, func(a, b int) bool {
		if affected[a].Start != affected[b].Start {
			return affected[a].Start < affected[b].Start
		}
		return affected[a].Occurrence < affected[b].Occurrence
	})

	type snapshot struct {
		t   *Treatment
		cid string
		inf Infection
	}
	saved := make([]snapshot, len(affected))
	for i, t := range affected {
		saved[i] = snapshot{t: t, cid: t.ChairID, inf: t.InfectionAtStart}
		s.chairTimelines[t.ChairID].Remove(t)
		s.patientTimelines[t.PatientID].Remove(t)
		delete(s.treats, t.ID)
	}

	reqs := make([]*placementRequest, len(affected))
	for i, t := range affected {
		reqs[i] = &placementRequest{item: t, locked: t.ChairID}
	}

	if err := s.admit(reqs); err != nil {
		// admit discards its own trial; restore every item as it was.
		for _, sn := range saved {
			sn.t.ChairID = sn.cid
			sn.t.InfectionAtStart = sn.inf
			s.putLive(sn.t)
		}
		return err
	}
	return nil
}

// FaultChair reports a chair fault [at, until). Treatments starting at or
// after at must be reassigned; treatments already in progress (start < at <
// end) are completed on the chair. If any affected treatment cannot be
// reassigned, the whole fault registration is rejected and nothing moves.
func (s *System) FaultChair(now, at, until int, chairID string) error {
	if err := checkID(chairID, "chair"); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	if err := checkTime(at); err != nil {
		return err
	}
	if err := checkTime(until); err != nil {
		return err
	}
	if until <= at {
		return errf(ErrInvalidArgument, "fault end %d must be after start %d", until, at)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	c, ok := s.chairs[chairID]
	if !ok {
		return errf(ErrNotFound, "chair %s unknown", chairID)
	}
	if !c.AvailableAt(now) {
		return errf(ErrStateConflict, "chair %s already in a fault window at now", chairID)
	}
	for i := range c.Faults {
		f := &c.Faults[i]
		// Reject windows that touch an already registered future window.
		if f.From < until && at < f.To {
			return errf(ErrStateConflict,
				"chair %s already has a fault window [%d,%d) overlapping [%d,%d)",
				chairID, f.From, f.To, at, until)
		}
	}

	// Collect treatments starting inside the new window.
	tl := s.chairTimelines[chairID]
	var affected []*Treatment
	cur := tl.Successor(at, nil)
	for cur != nil && cur.Start < until {
		affected = append(affected, cur)
		cur = tl.Successor(cur.Start+1, nil)
	}

	// Insert the window provisionally so feasibility treats the chair as
	// unavailable throughout [at,until). Save state for rollback.
	oldFaults := append([]FaultWindow(nil), c.Faults...)
	c.Faults = append(c.Faults, FaultWindow{From: at, To: until})
	sort.Slice(c.Faults, func(a, b int) bool {
		return c.Faults[a].From < c.Faults[b].From
	})

	if len(affected) > 0 {
		if err := s.reassignChair(affected); err != nil {
			c.Faults = oldFaults
			return err
		}
	}

	s.acceptClock(now)
	return nil
}

// RecoverChair clears the current fault window. Treatments already
// reassigned are never moved back.
func (s *System) RecoverChair(now int, chairID string) error {
	if err := checkID(chairID, "chair"); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	c, ok := s.chairs[chairID]
	if !ok {
		return errf(ErrNotFound, "chair %s unknown", chairID)
	}
	f := c.faultAt(now)
	if f == nil {
		// Allow recovery for a future-dated window only when now is before it.
		return errf(ErrStateConflict, "chair %s is not in a fault window at now", chairID)
	}
	// Shorten the current window so it ends at now.
	f.To = now
	s.acceptClock(now)
	return nil
}

// validStatusTransition reports whether moving to target is a legal change.
// Allowed: unknown -> negative / HBV / HCV (status determined), and
// negative -> HBV / HCV (new infection). Any other change is a state
// conflict.
func validStatusTransition(from, to Infection) bool {
	if from == to || !to.Valid() {
		return false
	}
	switch from {
	case InfectionUnknown:
		return to == InfectionNegative || to == InfectionHBV || to == InfectionHCV
	case InfectionNegative:
		return to == InfectionHBV || to == InfectionHCV
	default:
		return false
	}
}

// ChangeInfection determines a patient's pending status or records a new
// infection. Treatments starting at or after at are re-validated under the
// new state in ascending start order: the current chair is kept while still
// feasible, otherwise the smallest feasible chair is used. Treatments that
// already started (start < at) are untouched. Any single failure rejects
// the whole change and restores the previous state.
func (s *System) ChangeInfection(now, at int, patientID string, to Infection) error {
	if err := checkID(patientID, "patient"); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	if err := checkTime(at); err != nil {
		return err
	}
	if !to.Valid() {
		return errf(ErrInvalidArgument, "target infection state invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	p, ok := s.patients[patientID]
	if !ok {
		return errf(ErrNotFound, "patient %s unknown", patientID)
	}
	if !validStatusTransition(p.Infection, to) {
		return errf(ErrStateConflict,
			"cannot change infection %s -> %s", p.Infection, to)
	}

	tl := s.patientTimelines[patientID]
	var affected []*Treatment
	for cur := tl.Successor(at, nil); cur != nil; cur = tl.Successor(cur.Start+1, nil) {
		affected = append(affected, cur)
	}

	oldState := p.Infection
	saved := make([]struct {
		t   *Treatment
		inf Infection
		cid string
	}, len(affected))
	for i, t := range affected {
		saved[i].t = t
		saved[i].inf = t.InfectionAtStart
		saved[i].cid = t.ChairID
		s.chairTimelines[t.ChairID].Remove(t)
		s.patientTimelines[t.PatientID].Remove(t)
		delete(s.treats, t.ID)
		t.InfectionAtStart = to
	}
	p.Infection = to

	reqs := make([]*placementRequest, len(affected))
	for i, t := range affected {
		reqs[i] = &placementRequest{item: t, locked: saved[i].cid}
	}

	if err := s.admit(reqs); err != nil {
		for i, sn := range saved {
			sn.t.InfectionAtStart = sn.inf
			sn.t.ChairID = sn.cid
			_ = i
		}
		for _, sn := range saved {
			s.putLive(sn.t)
		}
		p.Infection = oldState
		return err
	}

	s.acceptClock(now)
	return nil
}

// CancelTreatment cancels one not-yet-started treatment and releases its
// chair together with the following disinfection occupancy. Already-started
// treatments are rejected with ErrStateConflict; no other treatment moves.
func (s *System) CancelTreatment(now int, treatmentID string) error {
	if err := checkID(treatmentID, "treatment"); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	t, ok := s.treats[treatmentID]
	if !ok {
		return errf(ErrNotFound, "treatment %s unknown", treatmentID)
	}
	if t.Start <= now {
		return errf(ErrStateConflict,
			"treatment %s already started at %d (now %d)", treatmentID, t.Start, now)
	}

	s.chairTimelines[t.ChairID].Remove(t)
	s.patientTimelines[t.PatientID].Remove(t)
	delete(s.treats, t.ID)

	if t.PlanID != "" {
		if p, ok := s.plans[t.PlanID]; ok && t.Occurrence >= 0 &&
			t.Occurrence < len(p.TreatmentIDs) {
			p.TreatmentIDs[t.Occurrence] = ""
		}
	}

	s.acceptClock(now)
	return nil
}

// CancelPlan cancels all not-yet-started occurrences of a plan. If any
// occurrence has already started the request is rejected as a whole with
// ErrStateConflict and nothing is cancelled.
func (s *System) CancelPlan(now int, planID string) error {
	if err := checkID(planID, "plan"); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	p, ok := s.plans[planID]
	if !ok {
		return errf(ErrNotFound, "plan %s unknown", planID)
	}
	if p.Cancelled {
		return errf(ErrStateConflict, "plan %s already cancelled", planID)
	}

	for _, id := range p.TreatmentIDs {
		if id == "" {
			continue
		}
		t := s.treats[id]
		if t == nil {
			continue
		}
		if t.Start <= now {
			return errf(ErrStateConflict,
				"plan %s has started treatment %s at %d", planID, id, t.Start)
		}
	}

	for _, id := range p.TreatmentIDs {
		if id == "" {
			continue
		}
		if t := s.treats[id]; t != nil {
			s.chairTimelines[t.ChairID].Remove(t)
			s.patientTimelines[t.PatientID].Remove(t)
			delete(s.treats, id)
		}
	}
	p.Cancelled = true
	for i := range p.TreatmentIDs {
		p.TreatmentIDs[i] = ""
	}
	s.acceptClock(now)
	return nil
}
