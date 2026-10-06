package hemo

// This file implements the public operations:
//
//	RegisterChair / RegisterPatient
//	AddPlan (weekly expansion + all-or-nothing allocation)
//	FaultChair / RecoverChair
//	ChangeInfection
//	CancelTreatment / CancelPlan
//	read-only snapshots
//
// Error reporting priority (only the first is returned):
//
//	invalid argument < clock rollback < not found < state conflict <
//	isolation conflict < patient conflict < no feasible chair.
//
// Rejected operations mutate nothing, including the clock.

func checkTime(t int) error {
	if t < MinTime || t > MaxTime {
		return errf(ErrInvalidArgument, "time %d out of range [0,%d]", t, MaxTime)
	}
	return nil
}

func checkDuration(d int) error {
	if d <= 0 {
		return errf(ErrInvalidArgument, "duration must be positive, got %d", d)
	}
	return nil
}

func checkID(id, what string) error {
	if id == "" {
		return errf(ErrInvalidArgument, "%s id must be non-empty", what)
	}
	return nil
}

// RegisterChair adds a chair to the center at operation time now.
func (s *System) RegisterChair(now int, id string, zone Zone, observation bool) error {
	if err := checkID(id, "chair"); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	if zone != ZoneNormal && zone != ZoneIsolation {
		return errf(ErrInvalidArgument, "zone must be normal or isolation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, exists := s.chairs[id]; exists {
		return errf(ErrStateConflict, "chair %s already registered", id)
	}
	s.chairs[id] = &Chair{
		ID:          id,
		Zone:        zone,
		Observation: zone == ZoneNormal && observation,
	}
	s.chairTimelines[id] = &Timeline{}
	s.acceptClock(now)
	return nil
}

// RegisterPatient adds a patient with an initial infection state.
func (s *System) RegisterPatient(now int, id string, infection Infection) error {
	if err := checkID(id, "patient"); err != nil {
		return err
	}
	if err := checkTime(now); err != nil {
		return err
	}
	if !infection.Valid() {
		return errf(ErrInvalidArgument, "infection state invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	if _, exists := s.patients[id]; exists {
		return errf(ErrStateConflict, "patient %s already registered", id)
	}
	s.patients[id] = &Patient{ID: id, Infection: infection}
	s.patientTimelines[id] = &Timeline{}
	s.acceptClock(now)
	return nil
}

// ChairView is a read-only snapshot of a chair.
type ChairView struct {
	ID           string
	Zone         Zone
	Observation  bool
	Available    bool
	Faults       []FaultWindow
	TreatmentIDs []string
}

// TreatmentView is a read-only snapshot of a treatment.
type TreatmentView struct {
	ID               string
	PatientID        string
	ChairID          string
	Start            int
	End              int
	Duration         int
	PlanID           string
	Occurrence       int
	InfectionAtStart Infection
}

// PlanView is a read-only snapshot of a plan.
type PlanView struct {
	ID           string
	PatientID    string
	Weekdays     [7]bool
	DayStart     int
	Duration     int
	ValidFrom    int
	ValidTo      int
	TreatmentIDs []string
	Cancelled    bool
}

func viewTreatment(t *Treatment) TreatmentView {
	return TreatmentView{
		ID:               t.ID,
		PatientID:        t.PatientID,
		ChairID:          t.ChairID,
		Start:            t.Start,
		End:              t.End,
		Duration:         t.Duration,
		PlanID:           t.PlanID,
		Occurrence:       t.Occurrence,
		InfectionAtStart: t.InfectionAtStart,
	}
}

// GetTreatment returns the snapshot of one live treatment.
func (s *System) GetTreatment(id string) (TreatmentView, error) {
	if err := checkID(id, "treatment"); err != nil {
		return TreatmentView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.treats[id]
	if !ok {
		return TreatmentView{}, errf(ErrNotFound, "treatment %s unknown", id)
	}
	return viewTreatment(t), nil
}

// GetPlan returns the snapshot of one plan.
func (s *System) GetPlan(id string) (PlanView, error) {
	if err := checkID(id, "plan"); err != nil {
		return PlanView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plans[id]
	if !ok {
		return PlanView{}, errf(ErrNotFound, "plan %s unknown", id)
	}
	ids := make([]string, len(p.TreatmentIDs))
	copy(ids, p.TreatmentIDs)
	return PlanView{
		ID:           p.ID,
		PatientID:    p.PatientID,
		Weekdays:     p.Weekdays,
		DayStart:     p.DayStart,
		Duration:     p.Duration,
		ValidFrom:    p.ValidFrom,
		ValidTo:      p.ValidTo,
		TreatmentIDs: ids,
		Cancelled:    p.Cancelled,
	}, nil
}

// PatientInfection returns the current infection state of a patient.
func (s *System) PatientInfection(id string) (Infection, error) {
	if err := checkID(id, "patient"); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.patients[id]
	if !ok {
		return 0, errf(ErrNotFound, "patient %s unknown", id)
	}
	return p.Infection, nil
}

// ChairTreatments returns the treatment IDs assigned to a chair in ascending
// start order.
func (s *System) ChairTreatments(chairID string) ([]string, error) {
	if err := checkID(chairID, "chair"); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tl, ok := s.chairTimelines[chairID]
	if !ok {
		return nil, errf(ErrNotFound, "chair %s unknown", chairID)
	}
	var ids []string
	first := tl.Successor(MinTime, nil)
	if first != nil {
		ids = append(ids, first.ID)
		cur := first.Start
		for {
			nxt := tl.Successor(cur+1, nil)
			if nxt == nil {
				break
			}
			ids = append(ids, nxt.ID)
			cur = nxt.Start
		}
	}
	return ids, nil
}
