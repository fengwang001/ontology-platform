package dtc

// dtcState 保存一个已登记故障码的全部生命周期状态。
type dtcState struct {
	code     int
	severity int

	// 跨循环持久状态
	pending               bool
	confirmed             bool
	healed                bool // 已愈合，作为历史保留
	occurrences           int
	consecutiveFailCycles int
	faultFreeWarmUps      int

	// 当前点火循环内的暂态
	deb             *debouncer
	failedThisCycle bool
	passedThisCycle bool
}

func newDTCState(code, severity int, cfg Config) *dtcState {
	return &dtcState{
		code:     code,
		severity: severity,
		deb:      newDebouncer(cfg),
	}
}

// beginCycle 点火开时复位循环内暂态。
func (s *dtcState) beginCycle() {
	s.deb.reset()
	s.failedThisCycle = false
	s.passedThisCycle = false
}

// endCycle 点火关时清零去抖（判定随之清零）。
func (s *dtcState) endCycle() {
	s.deb.reset()
	s.failedThisCycle = false
	s.passedThisCycle = false
}

// resetAll 诊断仪清除或自动清除时归零全部状态。
func (s *dtcState) resetAll() {
	s.pending = false
	s.confirmed = false
	s.healed = false
	s.occurrences = 0
	s.consecutiveFailCycles = 0
	s.faultFreeWarmUps = 0
	s.endCycle()
}
