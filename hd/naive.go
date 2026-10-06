package hd

import "sort"

// naiveModel 是与生产实现完全独立的朴素参考模型：不使用任何邻接索引，
// 每次判定都线性扫描该机位/该患者的全部未取消治疗，逐对检查重叠、消毒、
// 恢复、隔离与故障。用于差分测试：调度结论由两套独立代码分别得出，必须一致。

type nBay struct {
	zone     Zone
	observed bool
	avail    bool
}

type nTreatment struct {
	id, patient, bay string
	start, end       int
	inf              Infection
	plan             string
	virtual          bool
}

type nFault struct {
	start     int
	recovered bool
	recoverAt int
}

type nPlan struct {
	canceled bool
}

type naiveModel struct {
	cfg      Config
	bays     map[string]nBay
	patients map[string]Infection
	treats   map[string]*nTreatment
	plans    map[string]*nPlan
	faults   map[string]*nFault
	lastNow  int
	hasClock bool
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:      cfg,
		bays:     map[string]nBay{},
		patients: map[string]Infection{},
		treats:   map[string]*nTreatment{},
		plans:    map[string]*nPlan{},
		faults:   map[string]*nFault{},
	}
}

func (m *naiveModel) tick(now int) bool {
	return !(m.hasClock && now < m.lastNow)
}

func (m *naiveModel) commit(now int) { m.lastNow, m.hasClock = now, true }

func (m *naiveModel) sortedBay(bay string) []*nTreatment {
	var out []*nTreatment
	for _, t := range m.treats {
		if t.bay == bay {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].start != out[j].start {
			return out[i].start < out[j].start
		}
		return out[i].id < out[j].id
	})
	return out
}

func (m *naiveModel) patientList(p string) []*nTreatment {
	var out []*nTreatment
	for _, t := range m.treats {
		if t.patient == p {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].start != out[j].start {
			return out[i].start < out[j].start
		}
		return out[i].id < out[j].id
	})
	return out
}

func (m *naiveModel) zoneOK(b nBay, inf Infection) bool {
	switch inf {
	case InfectionHBV, InfectionHCV:
		return b.zone == ZoneIsolation
	case InfectionNegative:
		return b.zone == ZoneGeneral
	default:
		return b.zone == ZoneGeneral && b.observed
	}
}

// bayOK 朴素逐对检查。extra 为尚未提交的虚拟治疗，感染类型用其自身快照。
func (m *naiveModel) bayOK(bay string, inf Infection, start, end int, extra []*nTreatment) bool {
	b := m.bays[bay]
	if !m.zoneOK(b, inf) {
		return false
	}
	if !b.avail {
		if _, faulted := m.faults[bay]; !faulted {
			return false
		}
	}
	if f := m.faults[bay]; f != nil {
		rec := f.recoverAt
		if !f.recovered {
			rec = maxTime + 1
		}
		if start < rec && end > f.start {
			return false
		}
	}
	list := append(m.sortedBay(bay), extra...)
	for _, t := range list {
		// 感染类型直接使用治疗记录上的快照（生产实现亦如此）。
		tinf := t.inf
		if t.start == start {
			return false
		}
		if start > t.start {
			gap := m.cfg.cleanDuration(tinf)
			if tinf != inf {
				gap = m.cfg.DeepClean
			}
			if start < t.end+gap {
				return false
			}
		} else {
			gap := m.cfg.cleanDuration(inf)
			if inf != tinf {
				gap = m.cfg.DeepClean
			}
			if t.start < end+gap {
				return false
			}
		}
	}
	return true
}

func (m *naiveModel) patientOK(patient string, start, end int, extra []*nTreatment) bool {
	list := append(m.patientList(patient), extra...)
	var pred, succ *nTreatment
	for _, t := range list {
		if t.start < start {
			if pred == nil || t.start > pred.start {
				pred = t
			}
		} else if t.start > start {
			if succ == nil || t.start < succ.start {
				succ = t
			}
		} else {
			return false
		}
	}
	if pred != nil && start < pred.end+m.cfg.MinRecovery {
		return false
	}
	if succ != nil && succ.start < end+m.cfg.MinRecovery {
		return false
	}
	return true
}

func (m *naiveModel) sortedBayIDs() []string {
	ids := make([]string, 0, len(m.bays))
	for id := range m.bays {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *naiveModel) find(patient string, inf Infection, start, end int,
	extraByBay map[string][]*nTreatment, extraPatient []*nTreatment,
	forbid map[string]bool) (string, bool) {
	zoneAny := false
	for _, id := range m.sortedBayIDs() {
		if forbid[id] || !m.zoneOK(m.bays[id], inf) {
			continue
		}
		zoneAny = true
		if m.bayOK(id, inf, start, end, extraByBay[id]) {
			return id, zoneAny
		}
	}
	return "", zoneAny
}

func (m *naiveModel) registerBay(now int, id string, z Zone, observed bool) *Error {
	if id == "" || !z.valid() || !validTime(now) {
		return &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return &Error{Code: ErrClockRewind}
	}
	if _, ok := m.bays[id]; ok {
		return &Error{Code: ErrInvalidState}
	}
	m.bays[id] = nBay{zone: z, observed: observed, avail: true}
	m.commit(now)
	return nil
}

func (m *naiveModel) registerPatient(now int, id string, inf Infection) *Error {
	if id == "" || !inf.valid() || !validTime(now) {
		return &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return &Error{Code: ErrClockRewind}
	}
	if _, ok := m.patients[id]; ok {
		return &Error{Code: ErrInvalidState}
	}
	m.patients[id] = inf
	m.commit(now)
	return nil
}

func (m *naiveModel) book(now int, id, patient string, start, dur int) (string, *Error) {
	if id == "" || patient == "" || dur <= 0 || !validTime(now) || !validTime(start) ||
		start+dur > maxTime+1 {
		return "", &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return "", &Error{Code: ErrClockRewind}
	}
	if _, ok := m.treats[id]; ok {
		return "", &Error{Code: ErrInvalidState}
	}
	inf, ok := m.patients[patient]
	if !ok {
		return "", &Error{Code: ErrNotFound}
	}
	end := start + dur
	if !m.patientOK(patient, start, end, nil) {
		return "", &Error{Code: ErrPatientConflict}
	}
	bay, zoneAny := m.find(patient, inf, start, end, nil, nil, nil)
	if bay == "" {
		if zoneAny {
			return "", &Error{Code: ErrNoFeasibleBay}
		}
		return "", &Error{Code: ErrIsolationConflict}
	}
	m.treats[id] = &nTreatment{id: id, patient: patient, bay: bay, start: start, end: end, inf: inf}
	m.commit(now)
	return bay, nil
}

func (m *naiveModel) cancel(now int, id string) *Error {
	if id == "" || !validTime(now) {
		return &Error{Code: ErrInvalidArgument}
	}
	if !m.tick(now) {
		return &Error{Code: ErrClockRewind}
	}
	t, ok := m.treats[id]
	if !ok {
		return &Error{Code: ErrNotFound}
	}
	if t.start <= now {
		return &Error{Code: ErrInvalidState}
	}
	delete(m.treats, id)
	m.commit(now)
	return nil
}
