package hemo

import "strconv"

func itoa(i int) string { return strconv.Itoa(i) }

// expandPlan computes the occurrence starts of a weekly plan within the
// inclusive validity window. Weeks are anchored at time 0: weekday of minute
// t is (t/1440) mod 7, so weekday 0 starts at minutes 0, 10080, 20160, ...
// The returned starts are strictly ascending and unique.
func expandPlan(weekdays [7]bool, dayStart, validFrom, validTo int, duration int) ([]int, error) {
	if dayStart < 0 || dayStart >= MinutesPerDay {
		return nil, errf(ErrInvalidArgument,
			"day start %d outside [0,%d)", dayStart, MinutesPerDay)
	}
	any := false
	for _, w := range weekdays {
		if w {
			any = true
		}
	}
	if !any {
		return nil, errf(ErrInvalidArgument, "plan must select at least one weekday")
	}
	if validFrom > validTo {
		return nil, errf(ErrInvalidArgument, "valid from %d after valid to %d", validFrom, validTo)
	}

	firstDay := validFrom / MinutesPerDay
	lastDay := validTo / MinutesPerDay
	var starts []int
	for day := firstDay; day <= lastDay; day++ {
		if !weekdays[day%7] {
			continue
		}
		start := day*MinutesPerDay + dayStart
		end := start + duration
		// Occurrence must lie within the inclusive validity window, and its
		// last occupied minute (end-1) must not exceed the global time domain
		// (end may equal MaxTime+1 when the last occupied minute is MaxTime).
		if start < validFrom || end > validTo+1 || end > MaxTime+1 {
			continue
		}
		starts = append(starts, start)
	}
	return starts, nil
}

// AddPlan registers a weekly recurring plan and allocates chairs for every
// occurrence, all or nothing. The returned view is the stored plan.
//
// weekdays selects days with week anchored at minute 0; dayStart is minutes
// within the day; validity is inclusive. The plan ID is auto-generated.
func (s *System) AddPlan(now int, patientID string, weekdays [7]bool,
	dayStart, duration, validFrom, validTo int) (PlanView, error) {
	return s.AddPlanWithID(now, "", patientID, weekdays, dayStart, duration,
		validFrom, validTo)
}

// AddPlanWithID behaves like AddPlan but lets the caller pin the plan ID
// (used for deterministic differential testing). An empty id auto-generates.
func (s *System) AddPlanWithID(now int, planID, patientID string, weekdays [7]bool,
	dayStart, duration, validFrom, validTo int) (PlanView, error) {
	if planID != "" {
		if err := checkID(planID, "plan"); err != nil {
			return PlanView{}, err
		}
	}
	if err := checkID(patientID, "patient"); err != nil {
		return PlanView{}, err
	}
	if err := checkTime(now); err != nil {
		return PlanView{}, err
	}
	if err := checkDuration(duration); err != nil {
		return PlanView{}, err
	}
	if err := checkTime(validFrom); err != nil {
		return PlanView{}, err
	}
	if err := checkTime(validTo); err != nil {
		return PlanView{}, err
	}
	starts, err := expandPlan(weekdays, dayStart, validFrom, validTo, duration)
	if err != nil {
		return PlanView{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return PlanView{}, err
	}
	p, ok := s.patients[patientID]
	if !ok {
		return PlanView{}, errf(ErrNotFound, "patient %s unknown", patientID)
	}
	if planID != "" {
		if _, dup := s.plans[planID]; dup {
			return PlanView{}, errf(ErrStateConflict, "plan %s already exists", planID)
		}
	}

	// Empty expansion is legal (no occurrence in the window): store the plan
	// with zero treatments.
	plan := &Plan{
		ID:           planID,
		PatientID:    patientID,
		Weekdays:     weekdays,
		DayStart:     dayStart,
		Duration:     duration,
		ValidFrom:    validFrom,
		ValidTo:      validTo,
		TreatmentIDs: make([]string, 0, len(starts)),
	}
	if planID == "" {
		plan.ID = s.nextID("plan")
	}

	if len(starts) > 0 {
		reqs := make([]*placementRequest, len(starts))
		items := make([]*Treatment, len(starts))
		for i, start := range starts {
			id := "tr-" + plan.ID + "-" + itoa(i)
			if planID == "" {
				id = s.nextID("tr")
			}
			it := &Treatment{
				ID:               id,
				PatientID:        patientID,
				Start:            start,
				End:              start + duration,
				Duration:         duration,
				PlanID:           plan.ID,
				Occurrence:       i,
				InfectionAtStart: p.Infection,
			}
			items[i] = it
			reqs[i] = &placementRequest{item: it}
			plan.TreatmentIDs = append(plan.TreatmentIDs, id)
		}
		if err := s.admit(reqs); err != nil {
			return PlanView{}, err
		}
	}

	s.plans[plan.ID] = plan
	s.acceptClock(now)
	ids := make([]string, len(plan.TreatmentIDs))
	copy(ids, plan.TreatmentIDs)
	return PlanView{
		ID:           plan.ID,
		PatientID:    plan.PatientID,
		Weekdays:     plan.Weekdays,
		DayStart:     plan.DayStart,
		Duration:     plan.Duration,
		ValidFrom:    plan.ValidFrom,
		ValidTo:      plan.ValidTo,
		TreatmentIDs: ids,
	}, nil
}
