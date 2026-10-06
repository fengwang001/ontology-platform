// Package signal implements fixed-order signal timing control and
// green-wave (offset) coordination for a single arterial intersection.
package signal

import "errors"

// Distinguishable error classes, reported in the precedence order listed
// here (only the earliest applicable class is returned):
// invalid param -> clock rollback -> infeasible plan -> phase not found ->
// duplicate request -> consecutive skip -> target occupied.
var (
	ErrInvalidParam     = errors.New("signal: invalid parameter")
	ErrClockRollback    = errors.New("signal: clock rollback")
	ErrInfeasiblePlan   = errors.New("signal: infeasible plan")
	ErrPhaseNotFound    = errors.New("signal: phase not found")
	ErrDuplicateRequest = errors.New("signal: duplicate request")
	ErrConsecutiveSkip  = errors.New("signal: phase skipped in adjacent cycles")
	ErrTargetOccupied   = errors.New("signal: target phase occupied by another emergency request")
)

// PhaseSpec describes one phase of the intersection. All durations are
// positive integer seconds and MinGreen <= MaxGreen.
type PhaseSpec struct {
	MinGreen  int
	MaxGreen  int
	Clearance int
}

// Plan is a timing plan: per-phase set greens, the offset of this
// intersection relative to the arterial reference intersection, and the
// maximum total green adjustment allowed per cycle while resynchronizing.
type Plan struct {
	Greens            []int
	Offset            int
	MaxAdjustPerCycle int
}

// BusKind selects the kind of bus priority adjustment.
type BusKind int

const (
	BusExtend  BusKind = iota // extend current green, capped at MaxGreen
	BusShorten                // end current green early, floored at MinGreen
)

// ReqKind distinguishes emergency and bus priority requests.
type ReqKind int

const (
	ReqEmergency ReqKind = iota
	ReqBus
)

// RequestState is the lifecycle state of a priority request.
type RequestState string

const (
	StateQueued    RequestState = "Queued"
	StateServing   RequestState = "Serving"
	StateApplied   RequestState = "Applied"
	StateCompleted RequestState = "Completed"
	StatePreempted RequestState = "Preempted"
)

// Request is a recorded priority request.
type Request struct {
	ID     string
	Kind   ReqKind
	Target int // emergency target phase
	Bus    BusKind
	Delta  int
	Time   int
	State  RequestState
}

// QueryResult describes the signal state at a queried instant.
// Deviation is the signed offset deviation in seconds: positive means the
// actual cycle runs late relative to the plan schedule (greens will be
// shortened to resync), negative means it runs early (greens lengthened).
// A deviation of exactly half a cycle is reported positive and resyncs by
// lengthening.
type QueryResult struct {
	Phase     int
	Elapsed   int // seconds the current phase (green+clearance) has lasted
	Remaining int // seconds until the current phase ends
	Deviation int
}

func validateSpecs(specs []PhaseSpec) error {
	if len(specs) < 2 {
		return ErrInvalidParam
	}
	for _, s := range specs {
		if s.MinGreen <= 0 || s.MaxGreen <= 0 || s.Clearance <= 0 || s.MinGreen > s.MaxGreen {
			return ErrInvalidParam
		}
	}
	return nil
}

// validatePlan checks plan feasibility and returns the cycle length.
func validatePlan(specs []PhaseSpec, p Plan) (int, error) {
	if len(p.Greens) != len(specs) {
		return 0, ErrInfeasiblePlan
	}
	cycle := 0
	for i, g := range p.Greens {
		if g < specs[i].MinGreen || g > specs[i].MaxGreen {
			return 0, ErrInfeasiblePlan
		}
		cycle += g + specs[i].Clearance
	}
	if p.Offset < 0 || p.Offset >= cycle {
		return 0, ErrInfeasiblePlan
	}
	if p.MaxAdjustPerCycle < 0 {
		return 0, ErrInfeasiblePlan
	}
	return cycle, nil
}

func planCycle(specs []PhaseSpec, p Plan) int {
	c := 0
	for i, g := range p.Greens {
		c += g + specs[i].Clearance
	}
	return c
}

func clearanceSum(specs []PhaseSpec) int {
	c := 0
	for _, s := range specs {
		c += s.Clearance
	}
	return c
}

func modPos(a, m int) int {
	r := a % m
	if r < 0 {
		r += m
	}
	return r
}

// adjustedGreens computes the green vector for one traversal given the raw
// deviation r in [0, cycle). r == 0 means no adjustment; 0 < r < cycle/2
// shortens (cycle started late); r >= cycle/2 lengthens (tie at exactly
// half a cycle lengthens). The total adjustment is capped by
// MaxAdjustPerCycle and by per-phase min/max slack, distributed in phase
// order. It returns the adjusted greens and the applied adjustment.
func adjustedGreens(specs []PhaseSpec, p Plan, r int) ([]int, int) {
	g := append([]int(nil), p.Greens...)
	if r == 0 {
		return g, 0
	}
	cycle := planCycle(specs, p)
	var rem int
	lengthen := 2*r >= cycle
	if lengthen {
		rem = cycle - r
	} else {
		rem = r
	}
	if rem > p.MaxAdjustPerCycle {
		rem = p.MaxAdjustPerCycle
	}
	applied := rem
	for i := range g {
		if rem == 0 {
			break
		}
		var slack int
		if lengthen {
			slack = specs[i].MaxGreen - g[i]
		} else {
			slack = g[i] - specs[i].MinGreen
		}
		take := slack
		if take > rem {
			take = rem
		}
		if lengthen {
			g[i] += take
		} else {
			g[i] -= take
		}
		rem -= take
	}
	return g, applied - rem
}
