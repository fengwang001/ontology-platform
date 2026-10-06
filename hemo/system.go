package hemo

import (
	"fmt"
	"sort"
	"sync"
)

// System is the thread-safe stateful scheduling engine. All mutating
// operations are serialized through one mutex: the whole admission
// (including tentative placements) is atomic. Concurrent calls are thus
// equivalent to some serial order, and replaying the same call sequence
// deterministically reproduces identical chair assignments (chairs are
// always iterated in ascending ID order).
type System struct {
	mu      sync.Mutex
	cfg     Config
	lastNow int
	hasNow  bool
	seq     int64

	chairs   map[string]*Chair
	patients map[string]*Patient
	treats   map[string]*Treatment
	plans    map[string]*Plan

	// chairTimelines and patientTimelines index exactly the live treatments.
	chairTimelines   map[string]*Timeline
	patientTimelines map[string]*Timeline

	// pending is non-nil only during an atomic multi-placement admission.
	// Feasibility checks treat pending items as committed; they are all
	// committed together or all discarded.
	pending        map[string][]*Treatment // chairID -> tentative items
	patientPending map[string][]*Treatment // patientID -> tentative items

	// AuditProbes enables timeline probe accounting (see ProbeStats).
	AuditProbes bool
	probes      ProbeStats
}

// NewSystem constructs an engine with the given disinfection configuration.
func NewSystem(cfg Config) *System {
	return &System{
		cfg:              cfg,
		chairs:           map[string]*Chair{},
		patients:         map[string]*Patient{},
		treats:           map[string]*Treatment{},
		plans:            map[string]*Plan{},
		chairTimelines:   map[string]*Timeline{},
		patientTimelines: map[string]*Timeline{},
	}
}

// Config returns a copy of the engine configuration.
func (s *System) Config() Config { return s.cfg }

func (s *System) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s-%d", prefix, s.seq)
}

func (s *System) checkClock(now int) error {
	if s.hasNow && now < s.lastNow {
		return errf(ErrClockRollback, "now %d < last accepted now %d", now, s.lastNow)
	}
	return nil
}

func (s *System) acceptClock(now int) {
	s.lastNow = now
	s.hasNow = true
}

func (s *System) stats() *ProbeStats {
	if s.AuditProbes {
		return &s.probes
	}
	return nil
}

func sortedChairIDs(m map[string]*Chair) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// gapAfter returns the disinfection tail of an earlier treatment given the
// infection type of the next adjacent treatment: deep disinfection when HBV
// and HCV meet on the same isolation chair, regular otherwise.
func (s *System) gapAfter(prev, next Infection, zone Zone) int {
	if zone == ZoneIsolation &&
		((prev == InfectionHBV && next == InfectionHCV) ||
			(prev == InfectionHCV && next == InfectionHBV)) {
		return s.cfg.DeepDisinfect
	}
	return s.cfg.RegularGap(prev)
}

// zoneEligible reports whether a chair may serve a patient with infection v.
func zoneEligible(c *Chair, v Infection) bool {
	switch v {
	case InfectionHBV, InfectionHCV:
		return c.Zone == ZoneIsolation
	case InfectionNegative:
		return c.Zone == ZoneNormal
	default: // InfectionUnknown
		return c.Zone == ZoneNormal && c.Observation
	}
}

// rejectReason returns "" when c can host item, otherwise a short reason.
// Only the predecessor and successor of the candidate window are examined,
// so cost is independent of the total number of historical treatments.
func (s *System) rejectReason(c *Chair, item *Treatment, pendingOnChair []*Treatment) string {
	if !zoneEligible(c, item.InfectionAtStart) {
		return "zone/observation not eligible for infection state"
	}
	if !c.AvailableAt(item.Start) {
		return "chair unavailable due to fault"
	}

	tl := s.chairTimelines[c.ID]
	stats := s.stats()

	var pred, succ *Treatment
	if tl != nil {
		pred = tl.Predecessor(item.Start, stats)
		succ = tl.Successor(item.Start+1, stats)
	}
	for _, p := range pendingOnChair {
		if p == item {
			continue
		}
		if p.Start <= item.Start {
			if pred == nil || p.Start > pred.Start ||
				(p.Start == pred.Start && p.End > pred.End) {
				pred = p
			}
		} else if succ == nil || p.Start < succ.Start {
			succ = p
		}
	}

	if pred != nil {
		gap := s.gapAfter(pred.InfectionAtStart, item.InfectionAtStart, c.Zone)
		if pred.End > item.Start {
			return "overlaps earlier treatment"
		}
		if pred.End+gap > item.Start {
			return "disinfection tail of earlier treatment not finished"
		}
	}
	if succ != nil {
		gap := s.gapAfter(item.InfectionAtStart, succ.InfectionAtStart, c.Zone)
		if succ.Start < item.End {
			return "overlaps later treatment"
		}
		if item.End+gap > succ.Start {
			return "disinfection tail would delay later treatment"
		}
	}
	return ""
}

// patientConflict checks overlap/recovery rules against the patient's live
// timeline and already-pending items of the same admission.
func (s *System) patientConflict(patientID string, item *Treatment) *Treatment {
	tl := s.patientTimelines[patientID]
	stats := s.stats()
	var pred, succ *Treatment
	if tl != nil {
		pred = tl.Predecessor(item.Start, stats)
		succ = tl.Successor(item.Start+1, stats)
	}
	for _, p := range s.patientPending[patientID] {
		if p == item {
			continue
		}
		if p.Start <= item.Start {
			if pred == nil || p.Start > pred.Start ||
				(p.Start == pred.Start && p.End > pred.End) {
				pred = p
			}
		} else if succ == nil || p.Start < succ.Start {
			succ = p
		}
	}
	if pred != nil && pred.End+s.cfg.MinRecovery > item.Start {
		return pred
	}
	if succ != nil && item.End+s.cfg.MinRecovery > succ.Start {
		return succ
	}
	return nil
}

func errf(code ErrCode, format string, args ...any) error {
	return &OpError{Code: code, Detail: fmt.Sprintf(format, args...)}
}
