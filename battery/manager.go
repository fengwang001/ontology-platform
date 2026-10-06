package battery

import (
	"fmt"
	"sync"
)

// Snapshot is a complete, consistent state observed after one accepted sample.
type Snapshot struct {
	TimeMS              int64
	AcceptedSamples     int64
	AllowedChargeMA     int64
	AllowedDischargeMA  int64
	BanCharge           bool
	BanDischarge        bool
	BalanceRequest      bool
	SensorFault         bool
	Latched             bool
	LatchCauses         []FaultCause
	HasSampleSinceLatch bool
}

// Logger receives a human-readable trace for every accepted/rejected operation.
type Logger func(line string)

// Manager is the concurrent entry point of the battery protection manager.
// A single RWMutex serializes writes; readers observe a point-in-time copy.
type Manager struct {
	mu sync.RWMutex

	cfg      *Config
	log      Logger
	derate   *derateCalc
	prot     *protection
	delta    *deltaTracker
	oc       *overcurrentTracker
	latch    *latchState
	lastS    Sample
	haveLast bool

	lastMS           int64
	count            int64
	sampleSinceLatch bool
	last             Snapshot
}

// Option configures a Manager at construction.
type Option func(*Manager)

// WithLogger installs a trace logger invoked for every operation.
func WithLogger(l Logger) Option {
	return func(m *Manager) { m.log = l }
}

// New validates the configuration and constructs a Manager.
func New(cfg Config, opts ...Option) (*Manager, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	m := &Manager{
		cfg:    &cfg,
		derate: newDerateCalc(&cfg),
		prot:   newProtection(&cfg),
		delta:  newDeltaTracker(&cfg),
		oc:     newOvercurrentTracker(&cfg),
		latch:  newLatchState(),
	}
	for _, o := range opts {
		o(m)
	}
	m.last = m.snapshotLockedWith(0, cfg.RatedChargeMA, cfg.RatedDischargeMA, false, false, false, false)
	return m, nil
}

// Submit accepts or rejects one sample. A rejected sample changes no state and
// does not advance time.
func (m *Manager) Submit(s Sample) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.cfg.validateSample(s, m.lastMS, m.haveLast); err != nil {
		m.tracef("REJECT t=%d: %v | V=%v T=%v I=%d", s.TimeMS, err, s.CellVoltagesMV, s.Temperatures, s.CurrentMA)
		return Snapshot{}, err
	}

	// 1. Raw table limits.
	charge, discharge, sensorFault := m.derate.limits(s)

	// 2. Voltage protection states keep evolving unconditionally, even while
	//    a latch is active.
	banCharge, banDischarge := m.prot.update(s)

	// 3. Spread / balancing and its latching condition.
	balance, deltaLatch := m.delta.update(s)

	// 4. Overcurrent is judged against the limit as it stands this sample
	//    (protection bans and a sensor fault already zero the limits).
	ocLimitC, ocLimitD := charge, discharge
	if banCharge || sensorFault {
		ocLimitC = 0
	}
	if banDischarge || sensorFault {
		ocLimitD = 0
	}
	ocLatch := m.oc.update(s, ocLimitC, ocLimitD)

	// 5. Apply latch causes; multiple causes from one sample are all retained.
	wasLatched := m.latch.latched()
	newCauses := []FaultCause{}
	if ocLatch {
		m.latch.add(FaultOvercurrent)
		newCauses = append(newCauses, FaultOvercurrent)
	}
	if deltaLatch {
		m.latch.add(FaultVoltageDelta)
		newCauses = append(newCauses, FaultVoltageDelta)
	}

	if banCharge || sensorFault {
		charge = 0
	}
	if banDischarge || sensorFault {
		discharge = 0
	}
	if m.latch.latched() {
		charge, discharge = 0, 0
	}

	m.haveLast = true
	m.lastS = s
	m.lastMS = s.TimeMS
	m.count++
	if wasLatched {
		// A sample accepted while a latch was already present counts as the
		// "new sample after latch" required before a reset may succeed.
		m.sampleSinceLatch = true
	}

	m.last = m.snapshotLockedWith(s.TimeMS, charge, discharge, banCharge, banDischarge, balance, sensorFault)
	m.tracef("ACCEPT t=%d V=%v T=%v I=%d => chg=%d dis=%d banC=%v banD=%v bal=%v sensor=%v latch=%v new=%v",
		s.TimeMS, s.CellVoltagesMV, s.Temperatures, s.CurrentMA,
		charge, discharge, banCharge, banDischarge, balance, sensorFault, m.latch.causes, newCauses)
	return m.cloneSnapshot(m.last), nil
}

// CurrentSnapshot returns the current consistent state without advancing time.
func (m *Manager) CurrentSnapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cloneSnapshot(m.last)
}

// Reset attempts an operator-initiated latch reset.
func (m *Manager) Reset(operatorPermitted bool) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Required rejection order:
	// no permission > not latched > no sample since latch > recovery conditions.
	if !operatorPermitted {
		m.tracef("RESET reject: no permission")
		return Snapshot{}, ErrNoPermission
	}
	if !m.latch.latched() {
		m.tracef("RESET reject: not latched")
		return Snapshot{}, ErrNotLatched
	}
	if !m.sampleSinceLatch {
		m.tracef("RESET reject: no accepted sample after latch")
		return Snapshot{}, ErrNoSampleSinceLatch
	}

	s, ok := m.recoverySampleLocked()
	if !ok {
		m.tracef("RESET reject: recovery conditions not satisfied at t=%d (V=%v T=%v I=%d)",
			s.TimeMS, s.CellVoltagesMV, s.Temperatures, s.CurrentMA)
		return Snapshot{}, ErrRecoveryNotSatisfied
	}

	m.latch.clear()
	m.sampleSinceLatch = false
	m.oc.resetSustain()
	m.delta.resetSustain()
	// Re-derive the limits from the most recent sample: removing the latch
	// must restore table/protection limits without waiting for a new sample.
	charge, discharge, sensorFault := m.derate.limits(m.lastS)
	banCharge, banDischarge := m.prot.banCharge, m.prot.banDischarge
	balance := spreadExceeds(m.lastS.CellVoltagesMV, m.cfg.VoltageDeltaLimitMV)
	if banCharge || sensorFault {
		charge = 0
	}
	if banDischarge || sensorFault {
		discharge = 0
	}
	m.last = m.snapshotLockedWith(m.lastS.TimeMS, charge, discharge, banCharge, banDischarge, balance, sensorFault)
	m.tracef("RESET ok at t=%d", s.TimeMS)
	return m.cloneSnapshot(m.last), nil
}

func (m *Manager) recoverySampleLocked() (Sample, bool) {
	if !m.haveLast {
		return Sample{}, false
	}
	s := m.lastS
	cfg := m.cfg

	minV, maxV := s.CellVoltagesMV[0], s.CellVoltagesMV[0]
	for _, v := range s.CellVoltagesMV[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	noOV := maxV < cfg.OverVoltageMV
	noUV := minV > cfg.UnderVoltageMV
	noDelta := maxV-minV <= cfg.VoltageDeltaLimitMV

	validTemp := false
	for _, t := range s.Temperatures {
		if t != InvalidTemperature && t >= cfg.MinTemperature && t <= cfg.MaxTemperature {
			validTemp = true
			break
		}
	}
	rest := abs64(s.CurrentMA) <= cfg.RestCurrentThresholdMA

	return s, noOV && noUV && noDelta && validTemp && rest
}

func (m *Manager) snapshotLocked() Snapshot {
	return m.snapshotLockedWith(m.last.TimeMS, m.last.AllowedChargeMA, m.last.AllowedDischargeMA,
		m.last.BanCharge, m.last.BanDischarge, m.last.BalanceRequest, m.last.SensorFault)
}

func (m *Manager) snapshotLockedWith(timeMS, charge, discharge int64, banCharge, banDischarge, balance, sensorFault bool) Snapshot {
	var causes []FaultCause
	if m.latch.latched() {
		causes = append(causes, m.latch.causes...)
	}
	return Snapshot{
		TimeMS:              timeMS,
		AcceptedSamples:     m.count,
		AllowedChargeMA:     charge,
		AllowedDischargeMA:  discharge,
		BanCharge:           banCharge,
		BanDischarge:        banDischarge,
		BalanceRequest:      balance,
		SensorFault:         sensorFault,
		Latched:             m.latch.latched(),
		LatchCauses:         causes,
		HasSampleSinceLatch: m.sampleSinceLatch,
	}
}

func (m *Manager) cloneSnapshot(s Snapshot) Snapshot {
	if s.LatchCauses != nil {
		s.LatchCauses = append([]FaultCause(nil), s.LatchCauses...)
	}
	return s
}

func (m *Manager) tracef(format string, args ...any) {
	if m.log != nil {
		m.log(fmt.Sprintf(format, args...))
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
