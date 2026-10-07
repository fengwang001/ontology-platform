package dtc

import "sync"

// Snapshot 为单个故障码的查询结果。
type Snapshot struct {
	Code                  int
	Pending               bool
	Confirmed             bool
	Healed                bool
	JudgedFail            bool // 本次失败判定（当前去抖判定为失败）
	Occurrences           int
	ConsecutiveFailCycles int
	FaultFreeWarmUps      int
	OwnsFreezeFrame       bool
	DistanceSinceClear    int64 // 自清除基准起的行驶里程
}

// Manager 维护全部故障码的生命周期，所有方法可并发调用，
// 效果等价于某个串行顺序（内部以互斥锁串行化）。
type Manager struct {
	mu  sync.Mutex
	cfg Config

	ignitionOn   bool
	lastTime     int64
	lastOdometer int64
	clearBase    int64 // 清除基准里程

	dtcs map[int]*dtcState
	slot freezeFrameSlot

	// 环境采样：当前循环统计 + 最近一次采样（跨循环保持）
	hasSample bool
	minTemp   int64
	maxTemp   int64
	hasEnv    bool
	lastSpeed int64
	lastTemp  int64
}

// NewManager 创建管理器并校验配置。
func NewManager(cfg Config) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, dtcs: make(map[int]*dtcState)}, nil
}

// RegisterDTC 登记故障码，severity 取值 1..3。
func (m *Manager) RegisterDTC(code, severity int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if code <= 0 {
		return newError(ErrInvalidParam, "dtc code must be > 0, got %d", code)
	}
	if severity < 1 || severity > 3 {
		return newError(ErrInvalidParam, "severity must be in [1,3], got %d", severity)
	}
	if _, ok := m.dtcs[code]; ok {
		return newError(ErrInvalidParam, "dtc %d already registered", code)
	}
	m.dtcs[code] = newDTCState(code, severity, m.cfg)
	return nil
}

// Handle 处理一个事件；被拒绝的事件不改变任何状态。
func (m *Manager) Handle(ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.handleLocked(ev)
}

func (m *Manager) handleLocked(ev Event) error {
	// 1. 参数非法
	if err := validateEventParams(ev); err != nil {
		return err
	}
	// 2. 时刻或里程回退
	if ev.Time < m.lastTime || ev.Odometer < m.lastOdometer {
		return newError(ErrRegression, "event time/odometer (%d,%d) regresses below (%d,%d)",
			ev.Time, ev.Odometer, m.lastTime, m.lastOdometer)
	}
	// 3. 事件顺序非法
	if err := m.checkEventOrder(ev); err != nil {
		return err
	}
	// 4. 故障码未登记
	var target *dtcState
	if ev.Kind == EventMonitorResult {
		s, ok := m.dtcs[ev.DTCCode]
		if !ok {
			return newError(ErrDTCNotRegistered, "dtc %d not registered", ev.DTCCode)
		}
		target = s
	}
	// 5. 状态不允许
	if ev.Kind == EventScanToolClear && m.ignitionOn {
		return newError(ErrStateNotAllowed, "scan-tool clear requires ignition off")
	}

	// 全部校验通过，应用事件。
	m.lastTime = ev.Time
	m.lastOdometer = ev.Odometer
	switch ev.Kind {
	case EventIgnitionOn:
		m.ignitionOn = true
		m.hasSample = false
		m.minTemp, m.maxTemp = 0, 0
		for _, s := range m.dtcs {
			s.beginCycle()
		}
	case EventEnvironmentSample:
		if !m.hasSample {
			m.hasSample = true
			m.minTemp, m.maxTemp = ev.CoolantTemp, ev.CoolantTemp
		} else {
			if ev.CoolantTemp < m.minTemp {
				m.minTemp = ev.CoolantTemp
			}
			if ev.CoolantTemp > m.maxTemp {
				m.maxTemp = ev.CoolantTemp
			}
		}
		m.hasEnv = true
		m.lastSpeed = ev.Speed
		m.lastTemp = ev.CoolantTemp
	case EventMonitorResult:
		m.applyMonitorResult(target, ev)
	case EventIgnitionOff:
		m.settleCycle()
		m.ignitionOn = false
		for _, s := range m.dtcs {
			s.endCycle()
		}
	case EventScanToolClear:
		for _, s := range m.dtcs {
			s.resetAll()
		}
		m.slot.releaseAll()
		m.clearBase = ev.Odometer
	}
	return nil
}

func validateEventParams(ev Event) error {
	if ev.Time < 0 {
		return newError(ErrInvalidParam, "event time must be >= 0, got %d", ev.Time)
	}
	if ev.Odometer < 0 {
		return newError(ErrInvalidParam, "event odometer must be >= 0, got %d", ev.Odometer)
	}
	switch ev.Kind {
	case EventMonitorResult:
		if ev.DTCCode <= 0 {
			return newError(ErrInvalidParam, "monitor result dtc code must be > 0, got %d", ev.DTCCode)
		}
	case EventEnvironmentSample:
		if ev.Speed < 0 {
			return newError(ErrInvalidParam, "sample speed must be >= 0, got %d", ev.Speed)
		}
	}
	return nil
}

func (m *Manager) checkEventOrder(ev Event) error {
	switch ev.Kind {
	case EventIgnitionOn:
		if m.ignitionOn {
			return newError(ErrEventOrder, "ignition on while already on")
		}
	case EventIgnitionOff:
		if !m.ignitionOn {
			return newError(ErrEventOrder, "ignition off while already off")
		}
	case EventMonitorResult, EventEnvironmentSample:
		if !m.ignitionOn {
			return newError(ErrEventOrder, "%s requires ignition on", ev.Kind)
		}
	}
	return nil
}

// applyMonitorResult 处理一次监测上报：先去抖，再按判定驱动生命周期。
func (m *Manager) applyMonitorResult(s *dtcState, ev Event) {
	switch s.deb.report(ev.Passed) {
	case JudgmentPass:
		s.passedThisCycle = true
	case JudgmentFail:
		if s.failedThisCycle {
			return // 本循环已计首次失败，不再增加发生次数
		}
		s.failedThisCycle = true
		s.occurrences++
		s.pending = true
		if s.healed {
			// 已愈合的故障码再次失败：重新进入待定，计数从零开始。
			s.healed = false
			s.consecutiveFailCycles = 0
			s.faultFreeWarmUps = 0
		}
		m.slot.tryCapture(s.code, FreezeFrame{
			Speed:       m.lastSpeed,
			CoolantTemp: m.lastTemp,
			Odometer:    ev.Odometer,
		}, m.severityOf)
	}
}

func (m *Manager) severityOf(code int) int {
	return m.dtcs[code].severity
}

// settleCycle 点火关时的循环结算，开销只与已登记故障码数相关。
func (m *Manager) settleCycle() {
	warmUp := m.hasSample &&
		m.maxTemp-m.minTemp >= int64(m.cfg.WarmUpTempRise) &&
		m.maxTemp >= int64(m.cfg.WarmUpFinalTemp)
	for _, s := range m.dtcs {
		failed := s.failedThisCycle
		completedNoFail := s.passedThisCycle && !failed

		// 连续失败循环统计与确认。
		if failed {
			s.consecutiveFailCycles++
		} else if completedNoFail {
			s.consecutiveFailCycles = 0
		}
		if s.consecutiveFailCycles >= m.cfg.ConfirmCycles {
			s.confirmed = true
		}

		// 无故障暖机统计（仅已确认或已愈合的故障码）。
		if failed {
			s.faultFreeWarmUps = 0
		} else if warmUp && completedNoFail && (s.confirmed || s.healed) {
			s.faultFreeWarmUps++
		}

		// 愈合：清除确认与待定，作为历史保留。
		if s.confirmed && s.faultFreeWarmUps >= m.cfg.HealWarmUpCycles {
			s.confirmed = false
			s.pending = false
			s.healed = true
		}

		// 自动清除：已愈合历史码彻底清除，释放冻结帧。
		if s.healed && s.faultFreeWarmUps >= m.cfg.AutoClearWarmUps {
			s.resetAll()
			m.slot.releaseIfOwned(s.code)
		}
	}
}

// Snapshot 查询单个故障码状态。
func (m *Manager) Snapshot(code int) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.dtcs[code]
	if !ok {
		return Snapshot{}, newError(ErrDTCNotRegistered, "dtc %d not registered", code)
	}
	return m.snapshotOf(s), nil
}

// Snapshots 查询全部已登记故障码状态。
func (m *Manager) Snapshots() []Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Snapshot, 0, len(m.dtcs))
	for _, s := range m.dtcs {
		out = append(out, m.snapshotOf(s))
	}
	return out
}

func (m *Manager) snapshotOf(s *dtcState) Snapshot {
	return Snapshot{
		Code:                  s.code,
		Pending:               s.pending,
		Confirmed:             s.confirmed,
		Healed:                s.healed,
		JudgedFail:            s.deb.judgment == JudgmentFail,
		Occurrences:           s.occurrences,
		ConsecutiveFailCycles: s.consecutiveFailCycles,
		FaultFreeWarmUps:      s.faultFreeWarmUps,
		OwnsFreezeFrame:       m.slot.ownedBy(s.code),
		DistanceSinceClear:    m.lastOdometer - m.clearBase,
	}
}
