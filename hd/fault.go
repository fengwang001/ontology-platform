package hd

import "sort"

// ReportFault 登记机位故障：自 faultTime 起该机位停用，所有开始时刻不早于
// 故障时刻的治疗须改派。任一次改派不到则整体拒绝，所有治疗保持原状。
// 进行中的治疗（start < faultTime < end）视为在该机位完成，不受影响。
func (s *System) ReportFault(now int, bayID string, faultTime int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(bayID) {
		return errf(ErrInvalidArgument, "invalid bay id")
	}
	if !validTime(now) || !validTime(faultTime) {
		return errf(ErrInvalidArgument, "time out of range")
	}
	if err := s.clk.check(now); err != nil {
		return err
	}
	bay, ok := s.bays[bayID]
	if !ok {
		return errf(ErrNotFound, "bay %s not found", bayID)
	}
	if f := s.faults[bayID]; f != nil && !f.Recovered {
		return errf(ErrInvalidState, "bay %s already in fault since %d", bayID, f.Start)
	}

	var affected []*Treatment
	for _, t := range s.treatments {
		if t.BayID == bayID && t.Start >= faultTime {
			affected = append(affected, t)
		}
	}
	sort.Slice(affected, func(i, j int) bool { return affected[i].Start < affected[j].Start })

	// 先把受影响治疗从原机位/患者索引摘除，再在叠加视图上试改派；
	// 改派目标为其他机位（allowed 排除故障机位）。
	for _, t := range affected {
		s.detach(t)
	}
	allowed := map[string]bool{}
	for _, id := range s.bayOrder {
		allowed[id] = id != bayID
	}
	v := newOverlay(s)
	type move struct {
		t      *Treatment
		newBay string
	}
	var moves []move
	failed := false
	for _, t := range affected {
		inf := t.Infection
		if s.patientConflict(v, t.PatientID, t.Start, t.End) {
			failed = true
			break
		}
		newBay, _, feasible := s.findBay(v, t.PatientID, inf, t.Start, t.End, allowed)
		if !feasible {
			failed = true
			break
		}
		mt := *t
		mt.BayID = newBay
		v.add(&mt)
		moves = append(moves, move{t: t, newBay: newBay})
	}

	if failed {
		// 回滚：此前仅做了 detach，重新挂回即恢复原状，故障不登记。
		for _, t := range affected {
			s.reattach(t)
		}
		return errf(ErrNoFeasibleBay, "fault on bay %s rejected: reassign impossible", bayID)
	}

	for _, m := range moves {
		m.t.BayID = m.newBay
		s.reattach(m.t)
	}
	s.faults[bayID] = &Fault{BayID: bayID, Start: faultTime, RecoverAt: -1}
	bay.Available = false
	s.clk.accept(now)
	return nil
}

// RecoverBay 恢复机位可用。不回头调整已改派的治疗。
func (s *System) RecoverBay(now int, bayID string, recoverTime int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(bayID) {
		return errf(ErrInvalidArgument, "invalid bay id")
	}
	if !validTime(now) || !validTime(recoverTime) {
		return errf(ErrInvalidArgument, "time out of range")
	}
	if err := s.clk.check(now); err != nil {
		return err
	}
	bay, ok := s.bays[bayID]
	if !ok {
		return errf(ErrNotFound, "bay %s not found", bayID)
	}
	f := s.faults[bayID]
	if f == nil || f.Recovered {
		return errf(ErrInvalidState, "bay %s is not in fault", bayID)
	}
	if recoverTime < f.Start {
		return errf(ErrInvalidArgument, "recover time before fault time")
	}
	f.Recovered = true
	f.RecoverAt = recoverTime
	bay.Available = true
	s.clk.accept(now)
	return nil
}

// detach 将治疗从索引摘除但保留在 treatments 表中（改派/重核期间）。
func (s *System) detach(t *Treatment) {
	s.byBay[t.BayID].committed.remove(t.Start)
	s.byPatient[t.PatientID].committed.remove(t.Start)
}

// reattach 按当前字段重新挂回索引。
func (s *System) reattach(t *Treatment) {
	s.byBay[t.BayID].committed.insert(t)
	s.byPatient[t.PatientID].committed.insert(t)
}
