package fuzz_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"

	"ontology/hemo"
	"ontology/hemo/naive"
)

type opKind int

const (
	opRegChair opKind = iota
	opRegPatient
	opAddPlan
	opFault
	opRecover
	opChange
	opCancelTr
	opCancelPlan
)

type op struct {
	Kind                    opKind
	Now                     int
	ID, ID2                 string
	Zone                    naive.Zone
	Obs                     bool
	Inf, Inf2               naive.Infection
	Wk                      [7]bool
	DayStart, Dur, From, To int
	At, Until               int
}

type stepLog struct {
	Seq       int             `json:"seq"`
	Step      int             `json:"step"`
	Input     map[string]any  `json:"input"`
	Basis     string          `json:"basis"`
	ProdError string          `json:"prod_error"`
	NaiveErr  string          `json:"naive_error"`
	ProdCode  int             `json:"prod_code"`
	NaiveCode int             `json:"naive_code"`
	State     []assignmentRow `json:"state_after"`
	Patients  map[string]int  `json:"patients"`
}

type assignmentRow struct {
	Treatment string `json:"treatment"`
	Plan      string `json:"plan"`
	Occ       int    `json:"occ"`
	Patient   string `json:"patient"`
	Chair     string `json:"chair"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Inf       int    `json:"inf"`
}

func bothConfigs() (hemo.Config, naive.Config) {
	c := hemo.Config{
		RegularNegative: 25, RegularHBV: 35, RegularHCV: 45,
		RegularUnknown: 15, DeepDisinfect: 100, MinRecovery: 50,
	}
	return c, naive.Config{
		RegularNegative: c.RegularNegative, RegularHBV: c.RegularHBV,
		RegularHCV: c.RegularHCV, RegularUnknown: c.RegularUnknown,
		DeepDisinfect: c.DeepDisinfect, MinRecovery: c.MinRecovery,
	}
}

func pZone(z naive.Zone) hemo.Zone {
	if z == naive.ZoneIsolation {
		return hemo.ZoneIsolation
	}
	return hemo.ZoneNormal
}

func codeProd(err error) int {
	if err == nil {
		return 0
	}
	var oe *hemo.OpError
	if errors.As(err, &oe) {
		return int(oe.Code)
	}
	return -1
}

func codeNaive(err error) int {
	if err == nil {
		return 0
	}
	if ne, ok := err.(*naive.Error); ok {
		return int(ne.Code)
	}
	return -1
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type generator struct {
	rng       *rand.Rand
	chairs    []string
	patients  []string
	plans     []string
	planCount int
	now       int
}

func (g *generator) bumpNow() int {
	if g.rng.Intn(5) != 0 {
		g.now += g.rng.Intn(400)
		if g.now > 5_000_000 {
			g.now = 5_000_000
		}
	}
	return g.now
}

func (g *generator) pick(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[g.rng.Intn(len(xs))]
}

func (g *generator) next() op {
	now := g.bumpNow()
	kinds := []opKind{opRegChair, opRegChair, opRegPatient, opRegPatient}
	if len(g.chairs) > 0 {
		kinds = append(kinds, opFault, opRecover)
	}
	if len(g.patients) > 0 && len(g.chairs) > 0 {
		kinds = append(kinds, opAddPlan, opAddPlan, opChange)
	}
	if len(g.plans) > 0 {
		kinds = append(kinds, opCancelTr, opCancelPlan)
	}
	k := kinds[g.rng.Intn(len(kinds))]
	o := op{Kind: k, Now: now}
	switch k {
	case opRegChair:
		o.ID = fmt.Sprintf("C%02d", len(g.chairs)+1)
		if g.rng.Intn(3) == 0 {
			o.Zone = naive.ZoneIsolation
		} else {
			o.Zone = naive.ZoneNormal
			o.Obs = g.rng.Intn(2) == 0
		}
	case opRegPatient:
		o.ID = fmt.Sprintf("P%02d", len(g.patients)+1)
		o.Inf = naive.Infection(g.rng.Intn(4))
	case opAddPlan:
		o.ID = g.pick(g.patients)
		g.planCount++
		o.ID2 = fmt.Sprintf("PL%05d", g.planCount)
		for i := range o.Wk {
			o.Wk[i] = g.rng.Intn(3) != 0
		}
		o.DayStart = g.rng.Intn(1440)
		o.Dur = 100 + g.rng.Intn(250)
		base := g.rng.Intn(20000)
		o.From = base
		o.To = base + g.rng.Intn(30000)
	case opFault:
		o.ID = g.pick(g.chairs)
		if g.rng.Intn(3) == 0 {
			o.At = now - g.rng.Intn(300)
			if o.At < 0 {
				o.At = 0
			}
		} else {
			o.At = now + g.rng.Intn(5000)
		}
		o.Until = o.At + 100 + g.rng.Intn(5000)
	case opRecover:
		o.ID = g.pick(g.chairs)
	case opChange:
		o.ID = g.pick(g.patients)
		o.Inf2 = naive.Infection(1 + g.rng.Intn(3))
		o.At = now
	case opCancelTr, opCancelPlan:
		o.ID2 = g.pick(g.plans)
	}
	return o
}

func describe(o op) string {
	switch o.Kind {
	case opRegChair:
		return "register chair with zone/observation"
	case opRegPatient:
		return "register patient with infection state"
	case opAddPlan:
		return "expand weekly plan; admit all-or-nothing; same-chair preference else greedy by start"
	case opFault:
		return "fault window: reassign treatments starting within window; reject all if any fails; in-progress untouched"
	case opRecover:
		return "end active fault window; no retroactive moves"
	case opChange:
		return "re-check treatments starting >= at under new infection; keep feasible chair else smallest feasible; all-or-nothing"
	case opCancelTr:
		return "cancel one future treatment; release chair and disinfection tail"
	case opCancelPlan:
		return "cancel whole plan only when no occurrence started"
	}
	return ""
}

func opInput(o op, chairs, patients map[string]bool) map[string]any {
	in := map[string]any{
		"op":  opName(o.Kind),
		"now": o.Now,
	}
	switch o.Kind {
	case opRegChair:
		in["id"] = o.ID
		in["zone"] = int(o.Zone)
		in["observation"] = o.Obs
		in["known"] = chairs[o.ID]
	case opRegPatient:
		in["id"] = o.ID
		in["infection"] = int(o.Inf)
		in["known"] = patients[o.ID]
	case opAddPlan:
		in["plan_id"] = o.ID2
		in["patient"] = o.ID
		in["weekdays"] = o.Wk
		in["day_start"] = o.DayStart
		in["duration"] = o.Dur
		in["valid_from"] = o.From
		in["valid_to"] = o.To
	case opFault:
		in["chair"] = o.ID
		in["at"] = o.At
		in["until"] = o.Until
	case opRecover:
		in["chair"] = o.ID
	case opChange:
		in["patient"] = o.ID
		in["to_infection"] = int(o.Inf2)
		in["at"] = o.At
	case opCancelTr:
		in["plan"] = o.ID2
	case opCancelPlan:
		in["plan"] = o.ID2
	}
	return in
}

func opName(k opKind) string {
	switch k {
	case opRegChair:
		return "register_chair"
	case opRegPatient:
		return "register_patient"
	case opAddPlan:
		return "add_plan"
	case opFault:
		return "fault_chair"
	case opRecover:
		return "recover_chair"
	case opChange:
		return "change_infection"
	case opCancelTr:
		return "cancel_treatment"
	case opCancelPlan:
		return "cancel_plan"
	}
	return "?"
}

func treatmentOfPlan(m *naive.Model, planID string) string {
	p := m.Plans()[planID]
	if p == nil {
		return ""
	}
	for _, id := range p.TreatmentIDs {
		if id != "" {
			if _, ok := m.Treats()[id]; ok {
				return id
			}
		}
	}
	return ""
}

func treatmentOfPlanProd(s *hemo.System, planID string) string {
	pv, err := s.GetPlan(planID)
	if err != nil {
		return ""
	}
	for _, id := range pv.TreatmentIDs {
		if id != "" {
			if _, err := s.GetTreatment(id); err == nil {
				return id
			}
		}
	}
	return ""
}

func prodRows(s *hemo.System) []assignmentRow {
	var rows []assignmentRow
	for _, cid := range prodChairIDs(s) {
		ids, _ := s.ChairTreatments(cid)
		for _, id := range ids {
			tv, _ := s.GetTreatment(id)
			rows = append(rows, assignmentRow{
				tv.ID, tv.PlanID, tv.Occurrence, tv.PatientID, tv.ChairID,
				tv.Start, tv.End, int(tv.InfectionAtStart),
			})
		}
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Treatment < rows[b].Treatment })
	return rows
}

func naiveRows(m *naive.Model) []assignmentRow {
	snap := m.Snapshot()
	rows := make([]assignmentRow, 0, len(snap.Assignments))
	for _, a := range snap.Assignments {
		rows = append(rows, assignmentRow{
			a.TreatmentID, a.PlanID, a.Occurrence, a.PatientID, a.ChairID,
			a.Start, a.End, int(a.Infection),
		})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Treatment < rows[b].Treatment })
	return rows
}

// prodChairIDs enumerates chair ids through the exported query surface.
func prodChairIDs(s *hemo.System) []string {
	// No public chair listing is required by clients; the fuzz test uses a
	// small exported helper.
	return hemo.ExportedChairIDs(s)
}

func patientStates(s *hemo.System) map[string]int {
	out := map[string]int{}
	for _, id := range hemo.ExportedPatientIDs(s) {
		inf, err := s.PatientInfection(id)
		if err == nil {
			out[id] = int(inf)
		}
	}
	return out
}

func diffRows(a, b []assignmentRow) string {
	if len(a) != len(b) {
		return fmt.Sprintf("row count %d vs %d\nA=%v\nB=%v", len(a), len(b), a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			return fmt.Sprintf("row %d differs:\nprod:  %+v\nnaive: %+v", i, a[i], b[i])
		}
	}
	return ""
}
