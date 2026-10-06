package hd

import "sort"

// ChangeInfection 将患者状态由待定确定为阴性/阳性，或记录阴性转阳性等新感染。
// 开始时刻不早于 changeTime 的未开始治疗按新状态重新核对：原机位仍可行则保持，
// 否则改派到编号最小可行机位；任一次失败则整次变更被拒绝并保持原状。
// 已开始/已完成的治疗不追溯。
func (s *System) ChangeInfection(now int, patientID string, newInf Infection, changeTime int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(patientID) || !newInf.valid() {
		return errf(ErrInvalidArgument, "invalid patient id or infection state")
	}
	if !validTime(now) || !validTime(changeTime) {
		return errf(ErrInvalidArgument, "time out of range")
	}
	if err := s.clk.check(now); err != nil {
		return err
	}
	patient, ok := s.patients[patientID]
	if !ok {
		return errf(ErrNotFound, "patient %s not found", patientID)
	}
	if newInf == InfectionPending {
		return errf(ErrInvalidArgument, "cannot change state back to pending")
	}
	if patient.Infection == newInf {
		s.clk.accept(now)
		return nil
	}

	var affected []*Treatment
	for _, t := range s.treatments {
		if t.PatientID == patientID && t.Start >= changeTime && t.Start > now {
			affected = append(affected, t)
		}
	}
	sort.Slice(affected, func(i, j int) bool {
		if affected[i].Start != affected[j].Start {
			return affected[i].Start < affected[j].Start
		}
		return affected[i].ID < affected[j].ID
	})

	for _, t := range affected {
		s.detach(t)
	}
	v := newOverlay(s)
	type move struct {
		t      *Treatment
		newBay string
	}
	var moves []move
	failedCode := ErrNoFeasibleBay
	failed := false
	for _, t := range affected {
		originalBay := t.BayID
		var chosen string
		if !s.patientConflict(v, patientID, t.Start, t.End) {
			if ob := s.bays[originalBay]; s.bayFeasible(v, ob, patientID, newInf, t.Start, t.End) {
				chosen = originalBay
			}
		}
		if chosen == "" {
			if s.patientConflict(v, patientID, t.Start, t.End) {
				failedCode = ErrPatientConflict
				failed = true
				break
			}
			bayID, zoneOK, feasible := s.findBay(v, patientID, newInf, t.Start, t.End, nil)
			if !feasible {
				if !zoneOK {
					failedCode = ErrIsolationConflict
				}
				failed = true
				break
			}
			chosen = bayID
		}
		mt := *t
		mt.BayID = chosen
		mt.Infection = newInf
		v.add(&mt)
		moves = append(moves, move{t: t, newBay: chosen})
	}

	if failed {
		for _, t := range affected {
			s.reattach(t)
		}
		return errf(failedCode, "infection change for %s rejected", patientID)
	}

	patient.Infection = newInf
	for _, m := range moves {
		m.t.BayID = m.newBay
		m.t.Infection = newInf
		s.reattach(m.t)
	}
	s.clk.accept(now)
	return nil
}
