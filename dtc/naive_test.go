package dtc

// naive is an independent reference implementation used only by tests.
// It keeps every accepted event and recomputes the complete state from
// scratch by replaying the log, so its cost grows with history length —
// the exact opposite of the incremental Manager. If both agree on every
// query after every event of long random sequences, the incremental
// bookkeeping is very likely faithful to the specification.
type naive struct {
	cfg    Config
	sevs   map[string]int
	events []Event // accepted events only

	ignitionOn bool // meta state for validation, updated on accept
}

func newNaive(cfg Config, sevs map[string]int) *naive {
	return &naive{cfg: cfg, sevs: sevs}
}

// handle validates the event with the same priority order as the
// Manager and appends it to the log when accepted.
func (n *naive) handle(ev Event) error {
	// 1. parameters
	switch ev.Kind {
	case EvIgnitionOn, EvIgnitionOff, EvClear:
	case EvMonitorResult:
		if ev.DTC == "" {
			return errf(ErrInvalidParam, "monitor result without DTC id")
		}
	case EvEnvSample:
		if ev.Speed < 0 {
			return errf(ErrInvalidParam, "negative speed")
		}
	default:
		return errf(ErrInvalidParam, "unknown kind")
	}
	if ev.Time < 0 || ev.Odometer < 0 {
		return errf(ErrInvalidParam, "negative time/odometer")
	}
	// 2. regression
	if len(n.events) > 0 {
		last := n.events[len(n.events)-1]
		if ev.Time < last.Time || ev.Odometer < last.Odometer {
			return errf(ErrRegression, "regression")
		}
	}
	// 3.-5. order / registration / state
	switch ev.Kind {
	case EvIgnitionOn:
		if n.ignitionOn {
			return errf(ErrBadSequence, "already on")
		}
	case EvIgnitionOff:
		if !n.ignitionOn {
			return errf(ErrBadSequence, "already off")
		}
	case EvMonitorResult, EvEnvSample:
		if !n.ignitionOn {
			return errf(ErrBadSequence, "ignition off")
		}
		if ev.Kind == EvMonitorResult {
			if _, ok := n.sevs[ev.DTC]; !ok {
				return errf(ErrUnknownDTC, "unknown DTC")
			}
		}
	case EvClear:
		if n.ignitionOn {
			return errf(ErrStateNotAllowed, "clear needs ignition off")
		}
	}
	n.events = append(n.events, ev)
	if ev.Kind == EvIgnitionOn {
		n.ignitionOn = true
	}
	if ev.Kind == EvIgnitionOff {
		n.ignitionOn = false
	}
	return nil
}

// --- full replay -----------------------------------------------------

type naiveDTC struct {
	pending, confirmed, healed bool
	occurrences                int
	consecFail                 int
	faultFree                  int
}

type naiveCycleDTC struct {
	value      int
	judgment   Judgment
	hadFail    bool
	hadPass    bool
	occCounted bool
}

type naiveCycle struct {
	dtcs             map[string]*naiveCycleDTC
	samples          int
	minCool, maxCool int
}

type naiveView struct {
	states    map[string]*naiveDTC
	judgments map[string]Judgment
	ff        FreezeFrame
	ffSev     int
	baseline  int64
	lastOdo   int64
}

func (n *naive) replay() *naiveView {
	v := &naiveView{
		states:    map[string]*naiveDTC{},
		judgments: map[string]Judgment{},
	}
	var cy *naiveCycle
	lastSpeed, lastCool := 0, 0

	stateOf := func(id string) *naiveDTC {
		st := v.states[id]
		if st == nil {
			st = &naiveDTC{}
			v.states[id] = st
		}
		return st
	}

	for _, ev := range n.events {
		switch ev.Kind {
		case EvIgnitionOn:
			cy = &naiveCycle{dtcs: map[string]*naiveCycleDTC{}}
		case EvEnvSample:
			if cy.samples == 0 {
				cy.minCool, cy.maxCool = ev.Coolant, ev.Coolant
			} else {
				if ev.Coolant < cy.minCool {
					cy.minCool = ev.Coolant
				}
				if ev.Coolant > cy.maxCool {
					cy.maxCool = ev.Coolant
				}
			}
			cy.samples++
			lastSpeed, lastCool = ev.Speed, ev.Coolant
		case EvMonitorResult:
			c := cy.dtcs[ev.DTC]
			if c == nil {
				c = &naiveCycleDTC{}
				cy.dtcs[ev.DTC] = c
			}
			if ev.Passed {
				c.value -= n.cfg.DebounceFallStep
				if c.value < n.cfg.DebouncePassLimit {
					c.value = n.cfg.DebouncePassLimit
				}
				if c.value == n.cfg.DebouncePassLimit {
					c.judgment = JudgmentPass
				}
			} else {
				c.value += n.cfg.DebounceRiseStep
				if c.value > n.cfg.DebounceFailLimit {
					c.value = n.cfg.DebounceFailLimit
				}
				if c.value == n.cfg.DebounceFailLimit {
					c.judgment = JudgmentFail
				}
			}
			v.judgments[ev.DTC] = c.judgment
			if c.judgment == JudgmentFail {
				c.hadFail = true
				st := stateOf(ev.DTC)
				if !c.occCounted {
					st.occurrences++
					c.occCounted = true
				}
				if !st.pending {
					st.pending = true
					if st.healed {
						st.healed = false
						st.consecFail = 0
					}
					sev := n.sevs[ev.DTC]
					if !v.ff.Occupied || sev > v.ffSev {
						v.ff = FreezeFrame{
							Occupied: true, Owner: ev.DTC,
							Speed: lastSpeed, Coolant: lastCool,
							Odometer: ev.Odometer, Time: ev.Time,
						}
						v.ffSev = sev
					}
				}
			}
			if c.judgment == JudgmentPass {
				c.hadPass = true
			}
		case EvIgnitionOff:
			warmup := cy.samples > 0 &&
				cy.maxCool-cy.minCool >= n.cfg.WarmupRise &&
				cy.maxCool >= n.cfg.WarmupFinalTemp
			for id, c := range cy.dtcs {
				st := v.states[id]
				if c.hadFail {
					st.faultFree = 0
					st.consecFail++
					if st.consecFail >= n.cfg.ConfirmCycles {
						st.confirmed = true
					}
				} else if c.hadPass && st != nil {
					st.consecFail = 0
					if warmup && (st.confirmed || st.healed) {
						st.faultFree++
						if st.confirmed && st.faultFree >= n.cfg.HealWarmupCycles {
							st.confirmed = false
							st.pending = false
							st.healed = true
						}
						if st.healed && st.faultFree >= n.cfg.AutoClearWarmups {
							delete(v.states, id)
							if v.ff.Occupied && v.ff.Owner == id {
								v.ff = FreezeFrame{}
								v.ffSev = 0
							}
						}
					}
				}
			}
			cy = nil
			v.judgments = map[string]Judgment{}
		case EvClear:
			v.states = map[string]*naiveDTC{}
			v.ff = FreezeFrame{}
			v.ffSev = 0
			v.baseline = ev.Odometer
		}
		v.lastOdo = ev.Odometer
	}
	return v
}

func (n *naive) query(id string) Snapshot {
	v := n.replay()
	s := Snapshot{DistanceSinceClear: v.lastOdo - v.baseline}
	if st := v.states[id]; st != nil {
		s.Pending = st.pending
		s.Confirmed = st.confirmed
		s.Healed = st.healed
		s.Occurrences = st.occurrences
		s.ConsecFailCycles = st.consecFail
		s.FaultFreeWarmups = st.faultFree
	}
	s.Judgment = v.judgments[id]
	s.HasFreezeFrame = v.ff.Occupied && v.ff.Owner == id
	return s
}

func (n *naive) freezeFrame() FreezeFrame {
	return n.replay().ff
}
