package hd

import "strings"

func nonempty(id string) bool { return id != "" && strings.TrimSpace(id) != "" }

func validTime(t int) bool { return t >= 0 && t <= maxTime }

// RegisterBay 登记机位。重复登记报状态不符。
func (s *System) RegisterBay(now int, id string, zone Zone, observed, available bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(id) || !zone.valid() {
		return errf(ErrInvalidArgument, "invalid bay id or zone")
	}
	if !validTime(now) {
		return errf(ErrInvalidArgument, "now out of range")
	}
	if err := s.clk.check(now); err != nil {
		return err
	}
	if _, ok := s.bays[id]; ok {
		return errf(ErrInvalidState, "bay %s already registered", id)
	}
	bay := &Bay{ID: id, Zone: zone, Observed: observed, Available: available}
	s.bays[id] = bay
	s.bayOrder = append(s.bayOrder, id)
	s.byBay[id] = &baySchedule{}
	s.clk.accept(now)
	return nil
}

func (z Zone) valid() bool { return z == ZoneGeneral || z == ZoneIsolation }

// RegisterPatient 登记患者。
func (s *System) RegisterPatient(now int, id string, inf Infection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(id) || !inf.valid() {
		return errf(ErrInvalidArgument, "invalid patient id or infection state")
	}
	if !validTime(now) {
		return errf(ErrInvalidArgument, "now out of range")
	}
	if err := s.clk.check(now); err != nil {
		return err
	}
	if _, ok := s.patients[id]; ok {
		return errf(ErrInvalidState, "patient %s already registered", id)
	}
	s.patients[id] = &Patient{ID: id, Infection: inf}
	s.byPatient[id] = &patientSchedule{}
	s.clk.accept(now)
	return nil
}

// BookTreatment 申请单次治疗，成功返回分配机位。
func (s *System) BookTreatment(now int, treatmentID, patientID string, start, duration int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(treatmentID) || !nonempty(patientID) || duration <= 0 ||
		!validTime(now) || !validTime(start) || start+duration > maxTime+1 {
		return "", errf(ErrInvalidArgument, "invalid treatment parameters")
	}
	if err := s.clk.check(now); err != nil {
		return "", err
	}
	if _, ok := s.treatments[treatmentID]; ok {
		return "", errf(ErrInvalidState, "treatment %s already exists", treatmentID)
	}
	patient, ok := s.patients[patientID]
	if !ok {
		return "", errf(ErrNotFound, "patient %s not found", patientID)
	}
	end := start + duration
	v := liveView{s: s}
	if s.patientConflict(v, patientID, start, end) {
		return "", errf(ErrPatientConflict, "patient %s has overlapping/too-soon treatment", patientID)
	}
	bayID, zoneOK, feasible := s.findBay(v, patientID, patient.Infection, start, end, nil)
	if !feasible {
		if !zoneOK {
			return "", errf(ErrIsolationConflict, "no zone-eligible bay for infection %s", patient.Infection)
		}
		return "", errf(ErrNoFeasibleBay, "no feasible bay for treatment %s", treatmentID)
	}
	t := &Treatment{
		ID: treatmentID, PatientID: patientID, BayID: bayID,
		Start: start, End: end, Duration: duration, Infection: patient.Infection,
	}
	s.commitTreatment(t)
	s.clk.accept(now)
	return bayID, nil
}

func (s *System) commitTreatment(t *Treatment) {
	s.treatments[t.ID] = t
	s.byBay[t.BayID].committed.insert(t)
	s.byPatient[t.PatientID].committed.insert(t)
}

func (s *System) eraseTreatment(t *Treatment) {
	s.byBay[t.BayID].committed.remove(t.Start)
	s.byPatient[t.PatientID].committed.remove(t.Start)
	delete(s.treatments, t.ID)
}

// CancelTreatment 取消单次治疗。已开始（start <= now）的治疗不可取消。
func (s *System) CancelTreatment(now int, treatmentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(treatmentID) || !validTime(now) {
		return errf(ErrInvalidArgument, "invalid cancel parameters")
	}
	if err := s.clk.check(now); err != nil {
		return err
	}
	t, ok := s.treatments[treatmentID]
	if !ok {
		return errf(ErrNotFound, "treatment %s not found", treatmentID)
	}
	if t.Start <= now {
		return errf(ErrInvalidState, "treatment %s already started", treatmentID)
	}
	s.eraseTreatment(t)
	s.clk.accept(now)
	return nil
}

// Treatment 查询治疗记录（含机位、感染状态快照）。
func (s *System) Treatment(id string) (*Treatment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.treatments[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// PatientInfection 查询患者当前感染状态。
func (s *System) PatientInfection(id string) (Infection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.patients[id]
	if !ok {
		return 0, false
	}
	return p.Infection, true
}
