package routewatch

type engine struct {
	cfg *Config
}

func newEngine(cfg *Config) *engine {
	return &engine{cfg: cfg}
}

// resimulate rewrites results[from:] using the current static configuration,
// the prefix results (which are immutable) and the set of confirmed actual
// arrivals (anchors). It runs in O(n-from); it never reads or writes indices
// below from, so its cost cannot grow with the prefix length.
//
// State carried across a leg is the last served location (fromIdx) together
// with the time the vehicle becomes available there (readyAt) and the
// accumulated continuous driving at that moment (driving).
func (e *engine) resimulate(results []StopResult, from int, anchors map[int]int64) {
	cfg := e.cfg
	var fromIdx int
	var readyAt, driving int64
	if from == 0 {
		fromIdx = 0
		readyAt = cfg.Departure
		driving = 0
	} else {
		// Find the nearest served location strictly before from. Skipped and
		// canceled points contribute neither time nor driving: the vehicle
		// jumps straight from that location using its table row. This scan is
		// a fixed prefix lookup done once at the single re-simulation entry
		// point and never touches stop configuration; see docs/design.md for
		// the O(suffix) argument and the O(1) boundary structure.
		j := from - 1
		for j >= 0 && !results[j].Valid {
			j--
		}
		if j < 0 {
			fromIdx = 0
			readyAt = cfg.Departure
			driving = 0
		} else {
			p := results[j]
			fromIdx = j + 1
			readyAt = p.Departure
			driving = p.Driving
		}
	}

	for i := from; i < len(cfg.Stops); i++ {
		stop := &cfg.Stops[i]
		r := &results[i]
		r.Index = i
		r.ID = stop.ID

		if stop.Canceled {
			r.Canceled = true
			r.Valid = false
			r.Status = StatusSkipped
			r.FromIndex = fromIdx
			r.Arrival, r.ServiceStart, r.Departure, r.Driving = 0, 0, 0, 0
			continue
		}
		r.Canceled = false

		anchor, hasAnchor := anchors[i]
		if !hasAnchor {
			anchor = -1
		}
		served := e.advance(stop, i, fromIdx, readyAt, driving, anchor)
		*r = served.row
		if served.row.Valid {
			fromIdx = i + 1
			readyAt = served.row.Departure
			driving = served.row.Driving
		} else {
			// Skipped: carried served location/state remain unchanged.
			r.Index = i
			r.ID = stop.ID
			r.FromIndex = fromIdx
		}
	}
}

type advanceOut struct {
	row StopResult
}

// advance evaluates one candidate stop reached directly from the last served
// location. The fatigue check is always based on the state at departure from
// that served location: a run of skipped points never causes the pre-departure
// rest to be applied more than once, because each candidate is evaluated
// independently against the same carried state and its own direct leg.
func (e *engine) advance(stop *Stop, i, fromIdx int, readyAt, drivingAtDeparture int64, anchor int64) advanceOut {
	cfg := e.cfg
	fromID := e.cfg.DepotID
	if fromIdx > 0 {
		fromID = e.cfg.Stops[fromIdx-1].ID
	}
	leg := e.cfg.Travel.Duration[[2]string{fromID, stop.ID}]
	leaveAt := readyAt
	driving := drivingAtDeparture
	rested := false
	// Equality with the cap is permitted, so the test is strictly greater.
	if driving+leg > cfg.MaxDriving {
		leaveAt += cfg.RestDuration
		driving = 0
		rested = true
	}
	arrival := leaveAt + leg
	if anchor >= 0 {
		arrival = anchor
	}

	row := StopResult{Index: i, ID: stop.ID, Arrival: arrival, FromIndex: fromIdx}
	late := arrival > stop.Window.Latest
	if late && stop.Kind == HardWindow {
		row.Status = StatusSkipped
		return advanceOut{row: row}
	}
	driving += leg

	start := arrival
	if arrival < stop.Window.Earliest {
		start = stop.Window.Earliest
	}
	departure := start + stop.Service

	row.Valid = true
	row.ServiceStart = start
	row.Departure = departure
	switch {
	case late:
		row.Status = StatusLate
	case start > arrival:
		row.Status = StatusWaited
	default:
		row.Status = StatusOnTime
	}

	// Waiting and service together make the dwell; a dwell at least as long as
	// the required rest resets accumulated continuous driving.
	if departure-arrival >= cfg.RestDuration {
		driving = 0
	}
	row.Driving = driving
	_ = rested
	return advanceOut{row: row}
}
