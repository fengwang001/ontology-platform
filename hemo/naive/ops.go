package naive

import (
	"sort"
	"strconv"
)

func itoa(i int) string { return strconv.Itoa(i) }

func (m *Model) RegisterChair(now int, id string, zone Zone, obs bool) error {
	if id == "" || !validTime(now) {
		return ce(CodeInvalid, "bad chair args")
	}
	if zone != ZoneNormal && zone != ZoneIsolation {
		return ce(CodeInvalid, "bad zone")
	}
	if err := m.clockOK(now); err != nil {
		return err
	}
	if _, ok := m.chairs[id]; ok {
		return ce(CodeState, "chair exists")
	}
	m.chairs[id] = &Chair{ID: id, Zone: zone,
		Observation: zone == ZoneNormal && obs}
	m.lastNow, m.hasNow = now, true
	return nil
}

func (m *Model) RegisterPatient(now int, id string, inf Infection) error {
	if id == "" || !validTime(now) || inf > HCV {
		return ce(CodeInvalid, "bad patient args")
	}
	if err := m.clockOK(now); err != nil {
		return err
	}
	if _, ok := m.patients[id]; ok {
		return ce(CodeState, "patient exists")
	}
	m.patients[id] = &Patient{ID: id, Infection: inf}
	m.lastNow, m.hasNow = now, true
	return nil
}

func expandPlan(wk [7]bool, dayStart, from, to, dur int) []int {
	var out []int
	for day := from / MinutesPerDay; day <= to/MinutesPerDay; day++ {
		if !wk[day%7] {
			continue
		}
		start := day*MinutesPerDay + dayStart
		if start < from || start+dur > to+1 || start+dur > MaxTime+1 {
			continue
		}
		out = append(out, start)
	}
	return out
}

func (m *Model) AddPlan(now int, pid string, wk [7]bool, dayStart, dur, from, to int) (string, error) {
	return m.AddPlanWithID(now, "", pid, wk, dayStart, dur, from, to)
}

// AddPlanWithID mirrors hemo.System.AddPlanWithID for differential tests.
func (m *Model) AddPlanWithID(now int, planID, pid string, wk [7]bool,
	dayStart, dur, from, to int) (string, error) {
	if pid == "" || !validTime(now) || dur <= 0 ||
		!validTime(from) || !validTime(to) ||
		dayStart < 0 || dayStart >= MinutesPerDay {
		return "", ce(CodeInvalid, "bad plan args")
	}
	any := false
	for _, w := range wk {
		if w {
			any = true
		}
	}
	if !any || from > to {
		return "", ce(CodeInvalid, "bad weekdays/window")
	}
	if err := m.clockOK(now); err != nil {
		return "", err
	}
	p, ok := m.patients[pid]
	if !ok {
		return "", ce(CodeNotFound, "patient %s", pid)
	}
	if planID != "" {
		if _, dup := m.plans[planID]; dup {
			return "", ce(CodeState, "plan exists")
		}
	}
	starts := expandPlan(wk, dayStart, from, to, dur)
	if planID == "" {
		planID = m.nextID("plan")
	}
	plan := &Plan{
		ID:        planID,
		PatientID: pid, Weekdays: wk, DayStart: dayStart,
		Duration: dur, ValidFrom: from, ValidTo: to,
		TreatmentIDs: make([]string, 0, len(starts)),
	}
	items := make([]*Treatment, len(starts))
	for i, st := range starts {
		id := "tr-" + planID + "-" + itoa(i)
		if planID == "" {
			id = m.nextID("tr")
		}
		items[i] = &Treatment{
			ID: id, PatientID: pid, Start: st, End: st + dur,
			Duration: dur, PlanID: plan.ID, Occurrence: i,
			InfectionAtStart: p.Infection,
		}
		plan.TreatmentIDs = append(plan.TreatmentIDs, id)
	}
	if err := m.admit(items, nil); err != nil {
		return "", err
	}
	m.plans[plan.ID] = plan
	m.lastNow, m.hasNow = now, true
	return plan.ID, nil
}

func (m *Model) FaultChair(now, at, until int, cid string) error {
	if cid == "" || !validTime(now) || !validTime(at) ||
		!validTime(until) || until <= at {
		return ce(CodeInvalid, "bad fault args")
	}
	if err := m.clockOK(now); err != nil {
		return err
	}
	c, ok := m.chairs[cid]
	if !ok {
		return ce(CodeNotFound, "chair %s", cid)
	}
	if !m.chairAvailable(c, now) {
		return ce(CodeState, "chair in fault window at now")
	}
	for _, f := range c.Faults {
		if f.From < until && at < f.To {
			return ce(CodeState, "overlapping future fault")
		}
	}
	var affected []*Treatment
	for _, t := range m.allOnChair(cid) {
		if t.Start >= at && t.Start < until {
			affected = append(affected, t)
		}
	}
	savedFaults := append([]Window(nil), c.Faults...)
	savedChairs := make(map[string]string, len(affected))
	for _, t := range affected {
		savedChairs[t.ID] = t.ChairID
		delete(m.treats, t.ID)
	}
	c.Faults = append(c.Faults, Window{at, until})
	sort.Slice(c.Faults, func(a, b int) bool { return c.Faults[a].From < c.Faults[b].From })

	sort.Slice(affected, func(a, b int) bool {
		if affected[a].Start != affected[b].Start {
			return affected[a].Start < affected[b].Start
		}
		return affected[a].Occurrence < affected[b].Occurrence
	})
	locked := map[*Treatment]string{}
	for _, t := range affected {
		locked[t] = cid
	}
	if err := m.admit(affected, locked); err != nil {
		c.Faults = savedFaults
		for _, t := range affected {
			t.ChairID = savedChairs[t.ID]
			m.treats[t.ID] = t
		}
		return err
	}
	m.lastNow, m.hasNow = now, true
	return nil
}

func (m *Model) RecoverChair(now int, cid string) error {
	if cid == "" || !validTime(now) {
		return ce(CodeInvalid, "bad recover args")
	}
	if err := m.clockOK(now); err != nil {
		return err
	}
	c, ok := m.chairs[cid]
	if !ok {
		return ce(CodeNotFound, "chair %s", cid)
	}
	found := false
	for i := range c.Faults {
		f := &c.Faults[i]
		if f.From <= now && now < f.To {
			f.To = now
			found = true
		}
	}
	if !found {
		return ce(CodeState, "no active fault window")
	}
	m.lastNow, m.hasNow = now, true
	return nil
}

func validTransition(from, to Infection) bool {
	if from == to || to > HCV {
		return false
	}
	switch from {
	case Unknown:
		return to == Negative || to == HBV || to == HCV
	case Negative:
		return to == HBV || to == HCV
	}
	return false
}

func (m *Model) ChangeInfection(now, at int, pid string, to Infection) error {
	if pid == "" || !validTime(now) || !validTime(at) || to > HCV {
		return ce(CodeInvalid, "bad change args")
	}
	if err := m.clockOK(now); err != nil {
		return err
	}
	p, ok := m.patients[pid]
	if !ok {
		return ce(CodeNotFound, "patient %s", pid)
	}
	if !validTransition(p.Infection, to) {
		return ce(CodeState, "transition %d->%d", p.Infection, to)
	}
	var affected []*Treatment
	for _, t := range m.allOnPatient(pid) {
		if t.Start >= at {
			affected = append(affected, t)
		}
	}
	sort.Slice(affected, func(a, b int) bool {
		if affected[a].Start != affected[b].Start {
			return affected[a].Start < affected[b].Start
		}
		return affected[a].Occurrence < affected[b].Occurrence
	})
	type snap struct {
		t   *Treatment
		inf Infection
		cid string
	}
	saved := make([]snap, len(affected))
	for i, t := range affected {
		saved[i] = snap{t, t.InfectionAtStart, t.ChairID}
		delete(m.treats, t.ID)
		t.InfectionAtStart = to
	}
	old := p.Infection
	p.Infection = to
	locked := map[*Treatment]string{}
	for i, t := range affected {
		locked[t] = saved[i].cid
	}
	if err := m.admit(affected, locked); err != nil {
		p.Infection = old
		for _, sn := range saved {
			sn.t.InfectionAtStart = sn.inf
			sn.t.ChairID = sn.cid
			m.treats[sn.t.ID] = sn.t
		}
		return err
	}
	m.lastNow, m.hasNow = now, true
	return nil
}

func (m *Model) CancelTreatment(now int, tid string) error {
	if tid == "" || !validTime(now) {
		return ce(CodeInvalid, "bad cancel args")
	}
	if err := m.clockOK(now); err != nil {
		return err
	}
	t, ok := m.treats[tid]
	if !ok {
		return ce(CodeNotFound, "treatment %s", tid)
	}
	if t.Start <= now {
		return ce(CodeState, "already started")
	}
	delete(m.treats, tid)
	if t.PlanID != "" {
		if p := m.plans[t.PlanID]; p != nil && t.Occurrence >= 0 &&
			t.Occurrence < len(p.TreatmentIDs) {
			p.TreatmentIDs[t.Occurrence] = ""
		}
	}
	m.lastNow, m.hasNow = now, true
	return nil
}

func (m *Model) CancelPlan(now int, pid2 string) error {
	if pid2 == "" || !validTime(now) {
		return ce(CodeInvalid, "bad cancelplan args")
	}
	if err := m.clockOK(now); err != nil {
		return err
	}
	p, ok := m.plans[pid2]
	if !ok {
		return ce(CodeNotFound, "plan %s", pid2)
	}
	if p.Cancelled {
		return ce(CodeState, "plan cancelled")
	}
	for _, id := range p.TreatmentIDs {
		if t := m.treats[id]; t != nil && t.Start <= now {
			return ce(CodeState, "has started treatment")
		}
	}
	for _, id := range p.TreatmentIDs {
		delete(m.treats, id)
	}
	p.Cancelled = true
	for i := range p.TreatmentIDs {
		p.TreatmentIDs[i] = ""
	}
	m.lastNow, m.hasNow = now, true
	return nil
}

// TAssignment is one normalized state row for differential comparison.
type TAssignment struct {
	TreatmentID string
	PlanID      string
	Occurrence  int
	PatientID   string
	ChairID     string
	Start       int
	End         int
	Infection   Infection
}

// Snapshot returns a canonical, order-independent description of all live
// treatments plus current patient states.
type Snapshot struct {
	Assignments []TAssignment
	Patients    map[string]Infection
	ChairFaults map[string][]Window
}

func (m *Model) Snapshot() Snapshot {
	snap := Snapshot{
		Patients:    map[string]Infection{},
		ChairFaults: map[string][]Window{},
	}
	for _, t := range m.treats {
		snap.Assignments = append(snap.Assignments, TAssignment{
			TreatmentID: t.ID, PlanID: t.PlanID, Occurrence: t.Occurrence,
			PatientID: t.PatientID, ChairID: t.ChairID,
			Start: t.Start, End: t.End, Infection: t.InfectionAtStart,
		})
	}
	sort.Slice(snap.Assignments, func(a, b int) bool {
		return snap.Assignments[a].TreatmentID < snap.Assignments[b].TreatmentID
	})
	for id, p := range m.patients {
		snap.Patients[id] = p.Infection
	}
	for id, c := range m.chairs {
		var fs []Window
		for _, f := range c.Faults {
			if f.To > f.From {
				fs = append(fs, f)
			}
		}
		sort.Slice(fs, func(a, b int) bool { return fs[a].From < fs[b].From })
		snap.ChairFaults[id] = fs
	}
	return snap
}

// Plans returns the stored plan set (for ID mapping checks).
func (m *Model) Plans() map[string]*Plan { return m.plans }

// Chairs / Patients accessors for the fuzzer.
func (m *Model) Chairs() map[string]*Chair     { return m.chairs }
func (m *Model) Patients() map[string]*Patient { return m.patients }
func (m *Model) Treats() map[string]*Treatment { return m.treats }
