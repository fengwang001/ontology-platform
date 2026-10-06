package battery

import "sync"

type Manager struct {
	mu     sync.RWMutex
	cfg    Config
	state  batteryState
	last   Snapshot
	latest Sample
}

func NewManager(cfg Config) (*Manager, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Manager{cfg: cloneConfig(cfg), state: newBatteryState()}, nil
}

func (m *Manager) Submit(sample Sample) (Snapshot, error) {
	if err := sample.validateShape(m.cfg); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.hasSample && sample.TimeMS <= m.state.lastTimeMS {
		return Snapshot{}, &Error{
			Kind:    ErrorTimeNotAdvancing,
			Message: "sample time must be strictly greater than the previous accepted sample",
		}
	}

	wasLatched := m.state.latched
	limits := m.state.evaluate(m.cfg, sample)
	m.latest = cloneSample(sample)
	m.state.hasSample = true
	m.state.lastTimeMS = sample.TimeMS
	if wasLatched {
		m.state.acceptedAfterLatch = true
	}
	snapshot := m.snapshotLocked(limits)
	m.last = snapshot
	return snapshot, nil
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.last
}

func (m *Manager) Reset(authorized bool) error {
	if !authorized {
		return &Error{Kind: ErrorUnauthorized, Message: "reset requires an authorized operator"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.latched {
		return &Error{Kind: ErrorNotLatched, Message: "there is no latched fault to reset"}
	}
	if !m.state.acceptedAfterLatch {
		return &Error{Kind: ErrorNoSampleAfterLatch, Message: "at least one sample must be accepted after latching"}
	}
	if !m.recoveryConditionsMetLocked() {
		return &Error{Kind: ErrorRecoveryNotMet, Message: "latest sample does not satisfy recovery conditions"}
	}

	m.state.latched = false
	m.state.acceptedAfterLatch = false
	m.state.latchReasons = nil
	m.state.deltaRunStartMS = -1
	m.state.overcurrentStartMS = -1
	m.state.overcurrentDirection = ""
	limits := calculateLimits(m.cfg, m.latest, minSampleVoltage(m.latest), maxSampleVoltage(m.latest))
	m.last = m.snapshotLocked(limits)
	return nil
}

func (m *Manager) snapshotLocked(limits currentLimits) Snapshot {
	return Snapshot{
		HasSample:               m.state.hasSample,
		TimeMS:                  m.state.lastTimeMS,
		ChargeCurrentLimitMA:    limits.charge,
		DischargeCurrentLimitMA: limits.discharge,
		ChargeProhibited:        m.state.chargeProhibited,
		DischargeProhibited:     m.state.dischargeProhibited,
		BalancingRequested:      m.state.balancingRequested,
		SensorFault:             m.state.sensorFault,
		Latched:                 m.state.latched,
		LatchReasons:            cloneReasons(m.state.latchReasons),
	}
}

func (m *Manager) recoveryConditionsMetLocked() bool {
	sample := m.latest
	minimumVoltage := minSampleVoltage(sample)
	maximumVoltage := maxSampleVoltage(sample)
	deltaVoltage := absoluteDifference(maximumVoltage, minimumVoltage)
	currentMagnitude := sample.CurrentMA
	if currentMagnitude < 0 {
		currentMagnitude = -currentMagnitude
	}
	return m.state.hasSample &&
		maximumVoltage < m.cfg.OvervoltageThresholdMV &&
		minimumVoltage > m.cfg.UndervoltageThresholdMV &&
		deltaVoltage <= m.cfg.DeltaVoltageLimitMV &&
		anyTemperatureValid(sample) &&
		currentMagnitude <= m.cfg.IdleCurrentThresholdMA
}

func minSampleVoltage(sample Sample) int64 {
	result := sample.VoltagesMV[0]
	for _, voltage := range sample.VoltagesMV[1:] {
		if voltage < result {
			result = voltage
		}
	}
	return result
}

func maxSampleVoltage(sample Sample) int64 {
	result := sample.VoltagesMV[0]
	for _, voltage := range sample.VoltagesMV[1:] {
		if voltage > result {
			result = voltage
		}
	}
	return result
}
