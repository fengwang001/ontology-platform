// Package naive is an intentionally slow, independent reference
// implementation of the hemodialysis scheduling rules. It stores every
// treatment in one flat map and decides feasibility by linearly scanning ALL
// historical treatments. It shares no data structure with the production
// package hemo, so agreement between the two across thousands of random
// operation sequences is strong evidence that both encode the same rules.
package naive

import (
	"fmt"
	"sort"
)

const (
	MinTime       = 0
	MaxTime       = 10_000_000
	MinutesPerDay = 1440
)

type Infection uint8

const (
	Unknown Infection = iota
	Negative
	HBV
	HCV
)

type Zone uint8

const (
	ZoneNormal Zone = iota
	ZoneIsolation
)

type Config struct {
	RegularNegative int
	RegularHBV      int
	RegularHCV      int
	RegularUnknown  int
	DeepDisinfect   int
	MinRecovery     int
}

type Window struct{ From, To int }

type Chair struct {
	ID          string
	Zone        Zone
	Observation bool
	Faults      []Window
}

type Patient struct {
	ID        string
	Infection Infection
}

type Treatment struct {
	ID               string
	PatientID        string
	ChairID          string
	Start, End       int
	Duration         int
	PlanID           string
	Occurrence       int
	InfectionAtStart Infection
}

type Plan struct {
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

type Code int

const (
	CodeInvalid Code = iota + 1
	CodeClock
	CodeNotFound
	CodeState
	CodeIsolation
	CodePatient
	CodeNoChair
)

type Error struct {
	Code   Code
	Detail string
}

func (e *Error) Error() string { return fmt.Sprintf("code %d: %s", e.Code, e.Detail) }

func ce(code Code, f string, a ...any) error {
	return &Error{Code: code, Detail: fmt.Sprintf(f, a...)}
}

// Model is the naive engine.
type Model struct {
	cfg      Config
	lastNow  int
	hasNow   bool
	seq      int64
	chairs   map[string]*Chair
	patients map[string]*Patient
	treats   map[string]*Treatment
	plans    map[string]*Plan
}

func New(cfg Config) *Model {
	return &Model{
		cfg:      cfg,
		chairs:   map[string]*Chair{},
		patients: map[string]*Patient{},
		treats:   map[string]*Treatment{},
		plans:    map[string]*Plan{},
	}
}

func (m *Model) nextID(p string) string {
	m.seq++
	return fmt.Sprintf("%s-%d", p, m.seq)
}

func (m *Model) clockOK(now int) error {
	if m.hasNow && now < m.lastNow {
		return ce(CodeClock, "rollback %d<%d", now, m.lastNow)
	}
	return nil
}

func validTime(t int) bool { return t >= MinTime && t <= MaxTime }

func (m *Model) chairAvailable(c *Chair, t int) bool {
	for _, f := range c.Faults {
		if f.From <= t && t < f.To {
			return false
		}
	}
	return true
}

func zoneOK(c *Chair, v Infection) bool {
	switch v {
	case HBV, HCV:
		return c.Zone == ZoneIsolation
	case Negative:
		return c.Zone == ZoneNormal
	default:
		return c.Zone == ZoneNormal && c.Observation
	}
}

func (m *Model) regular(v Infection) int {
	switch v {
	case Negative:
		return m.cfg.RegularNegative
	case HBV:
		return m.cfg.RegularHBV
	case HCV:
		return m.cfg.RegularHCV
	default:
		return m.cfg.RegularUnknown
	}
}

func (m *Model) gap(prev, next Infection, zone Zone) int {
	if zone == ZoneIsolation &&
		((prev == HBV && next == HCV) || (prev == HCV && next == HBV)) {
		return m.cfg.DeepDisinfect
	}
	return m.regular(prev)
}

// allOnChair returns EVERY live treatment on a chair (O(history) scan).
func (m *Model) allOnChair(chairID string) []*Treatment {
	var out []*Treatment
	for _, t := range m.treats {
		if t.ChairID == chairID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Start != out[b].Start {
			return out[a].Start < out[b].Start
		}
		return out[a].ID < out[b].ID
	})
	return out
}

func (m *Model) allOnPatient(pid string) []*Treatment {
	var out []*Treatment
	for _, t := range m.treats {
		if t.PatientID == pid {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Start != out[b].Start {
			return out[a].Start < out[b].Start
		}
		return out[a].ID < out[b].ID
	})
	return out
}

func (m *Model) chairFeasible(c *Chair, item *Treatment, extra []*Treatment) bool {
	if !m.chairAvailable(c, item.Start) || !zoneOK(c, item.InfectionAtStart) {
		return false
	}
	list := append(m.allOnChair(c.ID), extra...)
	for _, t := range list {
		if t == item {
			continue
		}
		if t.Start <= item.Start {
			if t.End+m.gap(t.InfectionAtStart, item.InfectionAtStart, c.Zone) > item.Start {
				return false
			}
		} else if item.End+m.gap(item.InfectionAtStart, t.InfectionAtStart, c.Zone) > t.Start {
			return false
		}
	}
	return true
}

func (m *Model) patientOK(pid string, item *Treatment, extra []*Treatment) bool {
	list := append(m.allOnPatient(pid), extra...)
	for _, t := range list {
		if t == item {
			continue
		}
		if t.Start <= item.Start {
			if t.End+m.cfg.MinRecovery > item.Start {
				return false
			}
		} else if item.End+m.cfg.MinRecovery > t.Start {
			return false
		}
	}
	return true
}

func (m *Model) sortedChairs() []*Chair {
	out := make([]*Chair, 0, len(m.chairs))
	for _, c := range m.chairs {
		out = append(out, c)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}

func filterChair(items []*Treatment, id string) []*Treatment {
	var out []*Treatment
	for _, it := range items {
		if it.ChairID == id {
			out = append(out, it)
		}
	}
	return out
}

// admit atomically places items, mirroring the production algorithm.
// locked maps item pointer -> forced chair (kept while feasible).
func (m *Model) admit(items []*Treatment, locked map[*Treatment]string) error {
	saved := make(map[string]*Treatment, len(m.treats))
	for k, v := range m.treats {
		saved[k] = v
	}

	var trial []*Treatment
	for _, it := range items {
		if !m.patientOK(it.PatientID, it, trial) {
			return ce(CodePatient, "patient %s conflict at %d", it.PatientID, it.Start)
		}
		trial = append(trial, it)
	}

	chairs := m.sortedChairs()
	if len(locked) == 0 {
		for _, c := range chairs {
			var perChair []*Treatment
			ok := true
			for _, it := range items {
				if !m.chairFeasible(c, it, perChair) {
					ok = false
					break
				}
				perChair = append(perChair, it)
			}
			if ok {
				for _, it := range items {
					it.ChairID = c.ID
					m.treats[it.ID] = it
				}
				return nil
			}
		}
	}

	order := make([]int, len(items))
	for i := range items {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := items[order[a]], items[order[b]]
		if x.Start != y.Start {
			return x.Start < y.Start
		}
		return order[a] < order[b]
	})

	var placed, pending []*Treatment
	eligible := false
	for _, idx := range order {
		it := items[idx]
		var candidates []*Chair
		seen := map[string]bool{}
		if lock := locked[it]; lock != "" {
			if lc := m.chairs[lock]; lc != nil {
				candidates = append(candidates, lc)
				seen[lc.ID] = true
			}
		}
		for _, c := range chairs {
			if !seen[c.ID] {
				candidates = append(candidates, c)
			}
		}
		var chosen *Chair
		for _, c := range candidates {
			if m.chairFeasible(c, it, filterChair(pending, c.ID)) {
				chosen = c
				break
			}
			if m.chairAvailable(c, it.Start) && zoneOK(c, it.InfectionAtStart) {
				eligible = true
			}
		}
		if chosen == nil {
			m.treats = saved
			for _, p := range placed {
				p.ChairID = ""
			}
			if eligible {
				return ce(CodeNoChair, "no feasible chair at %d", it.Start)
			}
			return ce(CodeIsolation, "no eligible chair at %d", it.Start)
		}
		it.ChairID = chosen.ID
		m.treats[it.ID] = it
		placed = append(placed, it)
		pending = append(pending, it)
	}
	return nil
}
