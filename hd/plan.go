package hd

import "sort"

// overlayView 在已提交索引之上叠加尚未提交的虚拟治疗，
// 供周期方案展开、故障改派、状态重核的"试分配"使用。
type overlayView struct {
	s        *System
	virtual  map[string][]*Treatment // bayID -> 按 start 升序
	vpatient map[string][]*Treatment // patientID -> 按 start 升序
}

func newOverlay(s *System) *overlayView {
	return &overlayView{s: s, virtual: map[string][]*Treatment{}, vpatient: map[string][]*Treatment{}}
}

func mergePred(a, b *Treatment) *Treatment {
	if a == nil {
		return b
	}
	if b == nil || a.Start > b.Start {
		return a
	}
	return b
}

func mergeSucc(a, b *Treatment) *Treatment {
	if a == nil {
		return b
	}
	if b == nil || a.Start < b.Start {
		return a
	}
	return b
}

func listPred(list []*Treatment, key int) *Treatment {
	idx := sort.Search(len(list), func(i int) bool { return list[i].Start >= key }) - 1
	if idx < 0 {
		return nil
	}
	return list[idx]
}

func listSucc(list []*Treatment, key int) *Treatment {
	idx := sort.Search(len(list), func(i int) bool { return list[i].Start > key })
	if idx >= len(list) {
		return nil
	}
	return list[idx]
}

func (v *overlayView) bayNeighbors(bayID string, start int) (*Treatment, *Treatment) {
	p1, s1 := v.s.byBay[bayID].committed.neighbors(start)
	list := v.virtual[bayID]
	return mergePred(p1, listPred(list, start)), mergeSucc(s1, listSucc(list, start))
}

func (v *overlayView) bayAt(bayID string, start int) *Treatment {
	if t := v.s.byBay[bayID].committed.at(start); t != nil {
		return t
	}
	if t := listPred(v.virtual[bayID], start+1); t != nil && t.Start == start {
		return t
	}
	return nil
}

func (v *overlayView) patientNeighbors(patientID string, start int) (*Treatment, *Treatment) {
	p1, s1 := v.s.byPatient[patientID].committed.neighbors(start)
	list := v.vpatient[patientID]
	return mergePred(p1, listPred(list, start)), mergeSucc(s1, listSucc(list, start))
}

func (v *overlayView) patientAt(patientID string, start int) *Treatment {
	if t := v.s.byPatient[patientID].committed.at(start); t != nil {
		return t
	}
	if t := listPred(v.vpatient[patientID], start+1); t != nil && t.Start == start {
		return t
	}
	return nil
}

func (v *overlayView) add(t *Treatment) {
	list := v.virtual[t.BayID]
	pos := sort.Search(len(list), func(i int) bool { return list[i].Start >= t.Start })
	v.virtual[t.BayID] = append(list, nil)
	copy(v.virtual[t.BayID][pos+1:], v.virtual[t.BayID][pos:])
	v.virtual[t.BayID][pos] = t

	plist := v.vpatient[t.PatientID]
	ppos := sort.Search(len(plist), func(i int) bool { return plist[i].Start >= t.Start })
	v.vpatient[t.PatientID] = append(plist, nil)
	copy(v.vpatient[t.PatientID][ppos+1:], v.vpatient[t.PatientID][ppos:])
	v.vpatient[t.PatientID][ppos] = t
}

func (v *overlayView) dropBay(bayID string) { v.virtual[bayID] = nil }

// occurrence 是方案展开后的一次具体治疗。
type occurrence struct {
	start    int
	duration int
}

// expandPlan 在有效日期区间内展开每周指定日的治疗，按开始时刻升序。
func expandPlan(p *Plan) []occurrence {
	var days []int
	for d := 0; d < 7; d++ {
		if p.Weekdays[d] {
			days = append(days, d)
		}
	}
	var out []occurrence
	for day := p.From; day <= p.To; day += minutesPerDay {
		wd := ((day/minutesPerDay)%7 + 7) % 7
		if !p.Weekdays[wd] {
			continue
		}
		out = append(out, occurrence{start: day + p.DayStart, duration: p.Duration})
	}
	return out
}

// ApplyPlan 登记并展开周期性治疗方案，为每次治疗分配机位，全有或全无。
func (s *System) ApplyPlan(now int, planID, patientID string, weekdays map[int]bool,
	dayStart, duration, from, to int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(planID) || !nonempty(patientID) {
		return nil, errf(ErrInvalidArgument, "invalid plan/patient id")
	}
	if duration <= 0 || dayStart < 0 || dayStart >= minutesPerDay {
		return nil, errf(ErrInvalidArgument, "invalid duration or day start")
	}
	validDays := len(weekdays) > 0
	for d := range weekdays {
		if d < 0 || d > 6 {
			validDays = false
		}
	}
	if !validDays {
		return nil, errf(ErrInvalidArgument, "weekdays must be non-empty subset of 0..6")
	}
	if from < 0 || to < from || to > maxTime || from%minutesPerDay != 0 || to%minutesPerDay != 0 {
		return nil, errf(ErrInvalidArgument, "invalid valid date range")
	}
	if !validTime(now) {
		return nil, errf(ErrInvalidArgument, "now out of range")
	}
	if dayStart+duration > minutesPerDay {
		return nil, errf(ErrInvalidArgument, "treatment crosses day boundary")
	}
	if err := s.clk.check(now); err != nil {
		return nil, err
	}
	if _, ok := s.plans[planID]; ok {
		return nil, errf(ErrInvalidState, "plan %s already exists", planID)
	}
	patient, ok := s.patients[patientID]
	if !ok {
		return nil, errf(ErrNotFound, "patient %s not found", patientID)
	}

	plan := &Plan{
		ID: planID, PatientID: patientID, Weekdays: weekdays, DayStart: dayStart,
		Duration: duration, From: from, To: to,
	}
	occ := expandPlan(plan)
	if len(occ) == 0 {
		return nil, errf(ErrInvalidArgument, "plan expands to no treatment")
	}

	alloc := s.solvePlan(patientID, patient.Infection, occ)
	if alloc == nil {
		return nil, errf(ErrNoFeasibleBay, "plan %s cannot be fully allocated", planID)
	}

	s.plans[planID] = plan
	for i, t := range alloc {
		t.ID = planID + "#" + itoa(i)
		t.PlanID = planID
		s.commitTreatment(t)
	}
	s.clk.accept(now)
	bays := make([]string, len(alloc))
	for i, t := range alloc {
		bays[i] = t.BayID
	}
	return bays, nil
}

// solvePlan 先尝试让全部次数落在同一机位（取编号最小者），否则逐次贪心。
// 任一方案下出现患者自身冲突或无机位，均视为整体不可行（全有或全无）。
func (s *System) solvePlan(patientID string, inf Infection, occ []occurrence) []*Treatment {
	// 第一阶段：同机位优先。
	for _, bayID := range s.bayOrder {
		bay := s.bays[bayID]
		v := newOverlay(s)
		var alloc []*Treatment
		ok := true
		for _, o := range occ {
			end := o.start + o.duration
			t := &Treatment{PatientID: patientID, BayID: bayID, Start: o.start,
				End: end, Duration: o.duration, Infection: inf}
			if s.patientConflict(v, patientID, o.start, end) ||
				!s.bayFeasible(v, bay, patientID, inf, o.start, end) {
				ok = false
				break
			}
			v.add(t)
			alloc = append(alloc, t)
		}
		if ok {
			return alloc
		}
	}

	// 第二阶段：逐次贪心，每次取编号最小可行机位。
	v := newOverlay(s)
	var alloc []*Treatment
	for _, o := range occ {
		end := o.start + o.duration
		if s.patientConflict(v, patientID, o.start, end) {
			return nil
		}
		bayID, _, feasible := s.findBay(v, patientID, inf, o.start, end, nil)
		if !feasible {
			return nil
		}
		t := &Treatment{PatientID: patientID, BayID: bayID, Start: o.start,
			End: end, Duration: o.duration, Infection: inf}
		v.add(t)
		alloc = append(alloc, t)
	}
	return alloc
}

// CancelPlan 取消整个方案（含未开始与已开始的全部次数）。
// 已开始的单次治疗不可单独取消，但整案取消允许；消毒占用同步释放。
func (s *System) CancelPlan(now int, planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !nonempty(planID) || !validTime(now) {
		return errf(ErrInvalidArgument, "invalid cancel parameters")
	}
	if err := s.clk.check(now); err != nil {
		return err
	}
	plan, ok := s.plans[planID]
	if !ok {
		return errf(ErrNotFound, "plan %s not found", planID)
	}
	if plan.Canceled {
		return errf(ErrInvalidState, "plan %s already canceled", planID)
	}
	var ts []*Treatment
	for _, t := range s.treatments {
		if t.PlanID == planID {
			ts = append(ts, t)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Start < ts[j].Start })
	for _, t := range ts {
		if t.Start <= now {
			return errf(ErrInvalidState, "plan %s has started treatment at %d", planID, t.Start)
		}
	}
	for _, t := range ts {
		s.eraseTreatment(t)
	}
	plan.Canceled = true
	s.clk.accept(now)
	return nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
