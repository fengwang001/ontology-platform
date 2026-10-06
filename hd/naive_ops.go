package hd

import "sort"

// applyPlan 的朴素实现：先尝试单一机位（编号升序），否则逐次贪心。
func (m *naiveModel) applyPlan(now int, id, patient string, days map[int]bool,
	dayStart, dur, from, to int) ([]string, *Error) {
	if id == "" || patient == "" || dur <= 0 || dayStart < 0 || dayStart >= minutesPerDay ||
		len(days) == 0 || !validTime(now) {
		return nil, &Error{Code: ErrInvalidArgument}
	}
	for d := range days {
		if d < 0 || d > 6 {
			return nil, &Error{Code: ErrInvalidArgument}
		}
	}
	if from < 0 || to < from || to > maxTime || from%minutesPerDay != 0 || to%minutesPerDay != 0 ||
		dayStart+dur > minutesPerDay {
		return nil, &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return nil, &Error{Code: ErrClockRewind}
	}
	if p := m.plans[id]; p != nil {
		return nil, &Error{Code: ErrInvalidState}
	}
	inf, ok := m.patients[patient]
	if !ok {
		return nil, &Error{Code: ErrNotFound}
	}

	plan := &Plan{Weekdays: days, DayStart: dayStart, Duration: dur, From: from, To: to}
	occ := expandPlan(plan)
	if len(occ) == 0 {
		return nil, &Error{Code: ErrInvalidArgument}
	}

	solve := func(singleBay string) ([]string, bool) {
		extraByBay := map[string][]*nTreatment{}
		var extraPatient []*nTreatment
		var res []string
		for idx, o := range occ {
			end := o.start + o.duration
			if !m.patientOK(patient, o.start, end, extraPatient) {
				return nil, false
			}
			candidates := m.sortedBayIDs()
			if singleBay != "" {
				candidates = []string{singleBay}
			}
			chosen := ""
			for _, bid := range candidates {
				if !m.zoneOK(m.bays[bid], inf) {
					continue
				}
				if m.bayOK(bid, inf, o.start, end, extraByBay[bid]) {
					chosen = bid
					break
				}
			}
			if chosen == "" {
				return nil, false
			}
			nt := &nTreatment{id: id + "#" + itoa(idx), patient: patient, bay: chosen,
				start: o.start, end: end, inf: inf, plan: id, virtual: true}
			extraByBay[chosen] = append(extraByBay[chosen], nt)
			extraPatient = append(extraPatient, nt)
			res = append(res, chosen)
		}
		return res, true
	}

	var result []string
	for _, bid := range m.sortedBayIDs() {
		if res, ok := solve(bid); ok {
			result = res
			break
		}
	}
	if result == nil {
		res, ok := solve("")
		if !ok {
			return nil, &Error{Code: ErrNoFeasibleBay}
		}
		result = res
	}

	m.plans[id] = &nPlan{}
	for i, bid := range result {
		o := occ[i]
		tid := id + "#" + itoa(i)
		m.treats[tid] = &nTreatment{id: tid, patient: patient, bay: bid,
			start: o.start, end: o.start + o.duration, inf: inf, plan: id}
	}
	m.commit(now)
	return result, nil
}

func (m *naiveModel) cancelPlan(now int, id string) *Error {
	if id == "" || !validTime(now) {
		return &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return &Error{Code: ErrClockRewind}
	}
	p := m.plans[id]
	if p == nil {
		return &Error{Code: ErrNotFound}
	}
	if p.canceled {
		return &Error{Code: ErrInvalidState}
	}
	var ids []string
	for tid, t := range m.treats {
		if t.plan == id {
			ids = append(ids, tid)
		}
	}
	sort.Strings(ids)
	for _, tid := range ids {
		if m.treats[tid].start <= now {
			return &Error{Code: ErrInvalidState}
		}
	}
	for _, tid := range ids {
		delete(m.treats, tid)
	}
	p.canceled = true
	m.commit(now)
	return nil
}

func (m *naiveModel) cloneTreats() map[string]*nTreatment {
	cp := map[string]*nTreatment{}
	for k, v := range m.treats {
		t := *v
		cp[k] = &t
	}
	return cp
}

func (m *naiveModel) restoreTreats(saved map[string]*nTreatment) {
	m.treats = saved
}

func (m *naiveModel) reportFault(now int, bay string, at int) *Error {
	if bay == "" || !validTime(now) || !validTime(at) {
		return &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return &Error{Code: ErrClockRewind}
	}
	if _, ok := m.bays[bay]; !ok {
		return &Error{Code: ErrNotFound}
	}
	if f := m.faults[bay]; f != nil && !f.recovered {
		return &Error{Code: ErrInvalidState}
	}

	var aff []*nTreatment
	for _, t := range m.treats {
		if t.bay == bay && t.start >= at {
			aff = append(aff, t)
		}
	}
	sort.Slice(aff, func(i, j int) bool {
		if aff[i].start != aff[j].start {
			return aff[i].start < aff[j].start
		}
		return aff[i].id < aff[j].id
	})

	saved := m.cloneTreats()
	for _, t := range aff {
		delete(m.treats, t.id)
	}
	extraByBay := map[string][]*nTreatment{}
	extraPatient := map[string][]*nTreatment{}
	for _, t := range aff {
		if !m.patientOK(t.patient, t.start, t.end, extraPatient[t.patient]) {
			m.restoreTreats(saved)
			return &Error{Code: ErrNoFeasibleBay}
		}
		forbid := map[string]bool{bay: true}
		newBay, _ := m.find(t.patient, t.inf, t.start, t.end, extraByBay,
			extraPatient[t.patient], forbid)
		if newBay == "" {
			m.restoreTreats(saved)
			return &Error{Code: ErrNoFeasibleBay}
		}
		nt := *t
		nt.bay = newBay
		nt.virtual = true
		extraByBay[newBay] = append(extraByBay[newBay], &nt)
		extraPatient[t.patient] = append(extraPatient[t.patient], &nt)
	}

	for _, t := range aff {
		// 从 extra 结构中取回最终机位并写回。
		for _, list := range extraByBay {
			for _, nt := range list {
				if nt.id == t.id {
					committed := *nt
					committed.virtual = false
					m.treats[t.id] = &committed
				}
			}
		}
	}
	m.faults[bay] = &nFault{start: at, recoverAt: -1}
	m.commit(now)
	return nil
}

func (m *naiveModel) recoverBay(now int, bay string, at int) *Error {
	if bay == "" || !validTime(now) || !validTime(at) {
		return &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return &Error{Code: ErrClockRewind}
	}
	if _, ok := m.bays[bay]; !ok {
		return &Error{Code: ErrNotFound}
	}
	f := m.faults[bay]
	if f == nil || f.recovered {
		return &Error{Code: ErrInvalidState}
	}
	if at < f.start {
		return &Error{Code: ErrInvalidArgument}
	}
	f.recovered = true
	f.recoverAt = at
	m.commit(now)
	return nil
}

func (m *naiveModel) changeInfection(now int, patient string, inf Infection, at int) *Error {
	if patient == "" || !inf.valid() || inf == InfectionPending || !validTime(now) || !validTime(at) {
		return &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return &Error{Code: ErrClockRewind}
	}
	oldInf, ok := m.patients[patient]
	if !ok {
		return &Error{Code: ErrNotFound}
	}
	if oldInf == inf {
		m.commit(now)
		return nil
	}

	var aff []*nTreatment
	for _, t := range m.treats {
		if t.patient == patient && t.start >= at && t.start > now {
			aff = append(aff, t)
		}
	}
	sort.Slice(aff, func(i, j int) bool {
		if aff[i].start != aff[j].start {
			return aff[i].start < aff[j].start
		}
		return aff[i].id < aff[j].id
	})

	saved := m.cloneTreats()
	for _, t := range aff {
		delete(m.treats, t.id)
	}
	extraByBay := map[string][]*nTreatment{}
	var extraPatient []*nTreatment
	code := ErrNoFeasibleBay
	for _, t := range aff {
		chosen := ""
		if m.bayOK(t.bay, inf, t.start, t.end, extraByBay[t.bay]) {
			chosen = t.bay
		}
		if chosen == "" {
			if !m.patientOK(patient, t.start, t.end, extraPatient) {
				code = ErrPatientConflict
				m.restoreTreats(saved)
				return &Error{Code: code}
			}
			nb, zoneAny := m.find(patient, inf, t.start, t.end, extraByBay, extraPatient, nil)
			if nb == "" {
				if !zoneAny {
					code = ErrIsolationConflict
				}
				m.restoreTreats(saved)
				return &Error{Code: code}
			}
			chosen = nb
		}
		nt := *t
		nt.bay = chosen
		nt.inf = inf
		nt.virtual = true
		extraByBay[chosen] = append(extraByBay[chosen], &nt)
		extraPatient = append(extraPatient, &nt)
	}

	m.patients[patient] = inf
	for _, t := range aff {
		// aff 中的治疗均为重核窗口内的未开始治疗：未改派者保留原机位，
		// 但感染快照更新为新状态；改派者使用试分配得到的新机位。
		existing := *t
		existing.inf = inf
		var chosen *nTreatment
		for _, list := range extraByBay {
			for _, nt := range list {
				if nt.id == t.id {
					chosen = nt
				}
			}
		}
		if chosen != nil {
			existing = *chosen
		}
		existing.virtual = false
		m.treats[t.id] = &existing
	}
	m.commit(now)
	return nil
}
