package dtc

import "sync"

// Manager is the DTC lifecycle manager. All methods are safe for
// concurrent use; a single mutex serializes every operation, so any
// concurrent execution is equivalent to some serial order.
//
// Cost model: processing a monitor report is O(1) in the affected DTC,
// and ignition-off settlement iterates only the DTCs that reported
// during the closing cycle — never the accumulated history. The
// internal work counter (see WorkUnits) makes this verifiable.
type Manager struct {
	mu   sync.Mutex
	cfg  Config
	sevs map[string]int // registered DTCs: id -> severity (configuration)

	states map[string]*dtcState // lifecycle records (history)
	cycle  *cycleContext        // nil while ignition is off
	ff     freezeFrameSlot

	ignitionOn bool
	haveEvent  bool
	lastTime   int64
	lastOdo    int64

	clearBaseline int64

	// last environment sample, used for freeze frame captures.
	lastSpeed   int
	lastCoolant int

	workUnits int64 // diagnostic: per-DTC work performed (see WorkUnits)
}

// NewManager creates a Manager with the given configuration. An
// ErrInvalidParam error is returned for inconsistent calibration values.
func NewManager(cfg Config) (*Manager, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Manager{
		cfg:    cfg,
		sevs:   make(map[string]int),
		states: make(map[string]*dtcState),
	}, nil
}

// Register adds a DTC with the given severity (1..3, higher is more
// severe). Registration is configuration, not an event: it carries no
// timestamp and is allowed at any time.
func (m *Manager) Register(id string, severity int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" {
		return errf(ErrInvalidParam, "DTC id must not be empty")
	}
	if severity < 1 || severity > 3 {
		return errf(ErrInvalidParam, "severity of %q must be in [1,3], got %d", id, severity)
	}
	if _, dup := m.sevs[id]; dup {
		return errf(ErrInvalidParam, "DTC %q already registered", id)
	}
	m.sevs[id] = severity
	return nil
}

// Handle validates and applies one event. Rejected events never change
// any state. When several rules are violated, the error with the
// highest priority (smallest ErrCode) is returned.
func (m *Manager) Handle(ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. parameter validation
	if err := m.checkParams(ev); err != nil {
		return err
	}
	// 2. time / odometer regression
	if m.haveEvent && (ev.Time < m.lastTime || ev.Odometer < m.lastOdo) {
		return errf(ErrRegression, "event %s at (t=%d, odo=%d) regresses from (t=%d, odo=%d)",
			ev.Kind, ev.Time, ev.Odometer, m.lastTime, m.lastOdo)
	}
	// 3./4./5. order, registration and state checks, then apply
	var err error
	switch ev.Kind {
	case EvIgnitionOn:
		err = m.onIgnitionOn(ev)
	case EvIgnitionOff:
		err = m.onIgnitionOff(ev)
	case EvMonitorResult:
		err = m.onMonitorResult(ev)
	case EvEnvSample:
		err = m.onEnvSample(ev)
	case EvClear:
		err = m.onClear(ev)
	}
	if err != nil {
		return err
	}
	m.haveEvent = true
	m.lastTime = ev.Time
	m.lastOdo = ev.Odometer
	return nil
}

func (m *Manager) checkParams(ev Event) error {
	switch ev.Kind {
	case EvIgnitionOn, EvIgnitionOff, EvClear:
		// no extra fields
	case EvMonitorResult:
		if ev.DTC == "" {
			return errf(ErrInvalidParam, "monitor result without DTC id")
		}
	case EvEnvSample:
		if ev.Speed < 0 {
			return errf(ErrInvalidParam, "negative vehicle speed %d", ev.Speed)
		}
	default:
		return errf(ErrInvalidParam, "unknown event kind %d", int(ev.Kind))
	}
	if ev.Time < 0 {
		return errf(ErrInvalidParam, "negative event time %d", ev.Time)
	}
	if ev.Odometer < 0 {
		return errf(ErrInvalidParam, "negative odometer %d", ev.Odometer)
	}
	return nil
}

func (m *Manager) onIgnitionOn(Event) error {
	if m.ignitionOn {
		return errf(ErrBadSequence, "ignition-on while ignition is already on")
	}
	m.ignitionOn = true
	m.cycle = newCycleContext(m.cfg)
	return nil
}

func (m *Manager) onIgnitionOff(Event) error {
	if !m.ignitionOn {
		return errf(ErrBadSequence, "ignition-off while ignition is already off")
	}
	m.settleCycle()
	m.ignitionOn = false
	m.cycle = nil // debounce values and judgments die with the cycle
	return nil
}

func (m *Manager) onMonitorResult(ev Event) error {
	if !m.ignitionOn {
		return errf(ErrBadSequence, "monitor result while ignition is off")
	}
	sev, ok := m.sevs[ev.DTC]
	if !ok {
		return errf(ErrUnknownDTC, "monitor result for unregistered DTC %q", ev.DTC)
	}
	m.workUnits++
	e := m.cycle.entry(m.cfg, ev.DTC)
	switch e.deb.report(ev.Passed) {
	case JudgmentFail:
		e.hadFail = true
		st := m.states[ev.DTC]
		if st == nil {
			st = &dtcState{severity: sev}
			m.states[ev.DTC] = st
		}
		firstOfCycle := !e.occCounted
		e.occCounted = true
		if st.onFailJudgment(firstOfCycle) {
			// The DTC newly entered pending: compete for the freeze
			// frame slot with the latest environment data.
			m.ff.tryAcquire(ev.DTC, sev, FreezeFrame{
				Speed:    m.lastSpeed,
				Coolant:  m.lastCoolant,
				Odometer: ev.Odometer,
				Time:     ev.Time,
			})
		}
	case JudgmentPass:
		e.hadPass = true
	}
	return nil
}

func (m *Manager) onEnvSample(ev Event) error {
	if !m.ignitionOn {
		return errf(ErrBadSequence, "environment sample while ignition is off")
	}
	m.cycle.observeEnv(ev.Coolant)
	m.lastSpeed = ev.Speed
	m.lastCoolant = ev.Coolant
	return nil
}

func (m *Manager) onClear(ev Event) error {
	if m.ignitionOn {
		return errf(ErrStateNotAllowed, "tester clear requires ignition off")
	}
	// Erase all lifecycle state and history; registrations stay.
	m.states = make(map[string]*dtcState)
	m.ff.reset()
	m.clearBaseline = ev.Odometer
	return nil
}

// settleCycle closes the current ignition cycle: it counts consecutive
// failed cycles, confirmations, healing and auto-clear. Only DTCs that
// reported during this cycle are visited.
func (m *Manager) settleCycle() {
	warmup := m.cycle.isWarmup(m.cfg.WarmupRise, m.cfg.WarmupFinalTemp)
	for id, e := range m.cycle.entries {
		m.workUnits++
		st := m.states[id]
		switch {
		case e.hadFail:
			// st is guaranteed to exist: a failed judgment creates it.
			st.settleFailedCycle(m.cfg)
		case e.hadPass:
			// Monitoring completed without failure.
			if st != nil && st.settlePassedCycle(m.cfg, warmup) {
				delete(m.states, id)
				m.ff.releaseIfOwner(id)
			}
		}
		// Entries without any judgment cannot exist (a report always
		// produces a judgment when a limit is hit; hadFail/hadPass are
		// the only reasons an entry matters at settlement).
	}
}

// Query returns the current snapshot of a registered DTC. A registered
// DTC without any lifecycle history yields a zero snapshot.
func (m *Manager) Query(id string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sevs[id]; !ok {
		return Snapshot{}, errf(ErrUnknownDTC, "query for unregistered DTC %q", id)
	}
	j := JudgmentNone
	if m.cycle != nil {
		if e, ok := m.cycle.entries[id]; ok {
			j = e.deb.judgment
		}
	}
	distance := m.lastOdo - m.clearBaseline
	st := m.states[id]
	if st == nil {
		return Snapshot{Judgment: j, DistanceSinceClear: distance}, nil
	}
	return st.snapshot(j, m.ff.ownedBy(id), distance), nil
}

// FreezeFrame returns the current content of the single freeze frame slot.
func (m *Manager) FreezeFrame() FreezeFrame {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ff.frame
}

// WorkUnits returns the number of per-DTC work steps performed so far
// (debounce updates and settlement visits). It exists to make the cost
// model verifiable: a monitor report or an ignition-off settlement adds
// a number of units that depends only on the DTCs involved in that
// cycle, never on the accumulated history length.
func (m *Manager) WorkUnits() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workUnits
}
