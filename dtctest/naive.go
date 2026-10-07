// Package dtctest 提供一个独立编写的朴素模型，用于与 dtc.Manager 对照。
// 朴素模型保留全部已接受事件历史，每次查询都从头重放，
// 去抖判定按报告序列前缀整体重算，复杂度随历史长度增长——
// 这正与生产实现的 O(故障码数) 结算形成对照。
package dtctest

import (
	"ontology/dtc"
)

type report struct {
	code   int
	passed bool
}

type sample struct {
	speed int64
	temp  int64
}

type acc struct {
	severity              int
	pending               bool
	confirmed             bool
	healed                bool
	occurrences           int
	consecutiveFailCycles int
	faultFreeWarmUps      int
}

// Model 是朴素参考实现。
type Model struct {
	cfg    dtc.Config
	sev    map[int]int
	events []dtc.Event // 已接受事件完整历史

	ignitionOn   bool
	lastTime     int64
	lastOdometer int64
}

func NewModel(cfg dtc.Config) (*Model, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Model{cfg: cfg, sev: map[int]int{}}, nil
}

func (m *Model) RegisterDTC(code, severity int) error {
	if code <= 0 || severity < 1 || severity > 3 {
		return &dtc.Error{Kind: dtc.ErrInvalidParam, Detail: "bad registration"}
	}
	if _, ok := m.sev[code]; ok {
		return &dtc.Error{Kind: dtc.ErrInvalidParam, Detail: "duplicate"}
	}
	m.sev[code] = severity
	return nil
}

// Handle 校验并记录事件；校验次序与生产实现一致。
func (m *Model) Handle(ev dtc.Event) error {
	// 1. 参数
	if ev.Time < 0 || ev.Odometer < 0 {
		return &dtc.Error{Kind: dtc.ErrInvalidParam, Detail: "negative time/odometer"}
	}
	if ev.Kind == dtc.EventMonitorResult && ev.DTCCode <= 0 {
		return &dtc.Error{Kind: dtc.ErrInvalidParam, Detail: "bad dtc code"}
	}
	if ev.Kind == dtc.EventEnvironmentSample && ev.Speed < 0 {
		return &dtc.Error{Kind: dtc.ErrInvalidParam, Detail: "bad speed"}
	}
	// 2. 回退
	if ev.Time < m.lastTime || ev.Odometer < m.lastOdometer {
		return &dtc.Error{Kind: dtc.ErrRegression, Detail: "regression"}
	}
	// 3. 顺序
	switch ev.Kind {
	case dtc.EventIgnitionOn:
		if m.ignitionOn {
			return &dtc.Error{Kind: dtc.ErrEventOrder, Detail: "double on"}
		}
	case dtc.EventIgnitionOff:
		if !m.ignitionOn {
			return &dtc.Error{Kind: dtc.ErrEventOrder, Detail: "off while off"}
		}
	case dtc.EventMonitorResult, dtc.EventEnvironmentSample:
		if !m.ignitionOn {
			return &dtc.Error{Kind: dtc.ErrEventOrder, Detail: "needs ignition on"}
		}
	}
	// 4. 未登记
	if ev.Kind == dtc.EventMonitorResult {
		if _, ok := m.sev[ev.DTCCode]; !ok {
			return &dtc.Error{Kind: dtc.ErrDTCNotRegistered, Detail: "unknown dtc"}
		}
	}
	// 5. 状态
	if ev.Kind == dtc.EventScanToolClear && m.ignitionOn {
		return &dtc.Error{Kind: dtc.ErrStateNotAllowed, Detail: "clear needs ignition off"}
	}

	m.events = append(m.events, ev)
	m.lastTime = ev.Time
	m.lastOdometer = ev.Odometer
	if ev.Kind == dtc.EventIgnitionOn {
		m.ignitionOn = true
	}
	if ev.Kind == dtc.EventIgnitionOff {
		m.ignitionOn = false
	}
	return nil
}

// judgmentAfter 朴素地从头重算某故障码在一串报告后的去抖判定。
func (m *Model) judgmentAfter(reports []report, code int) (judgment dtc.Judgment, anyFail bool, anyPass bool) {
	value := 0
	j := dtc.JudgmentNone
	for _, r := range reports {
		if r.code != code {
			continue
		}
		if r.passed {
			value -= m.cfg.DebounceFallStep
			if value < m.cfg.DebouncePassLimit {
				value = m.cfg.DebouncePassLimit
			}
		} else {
			value += m.cfg.DebounceRiseStep
			if value > m.cfg.DebounceFailLimit {
				value = m.cfg.DebounceFailLimit
			}
		}
		if value >= m.cfg.DebounceFailLimit {
			j = dtc.JudgmentFail
		} else if value <= m.cfg.DebouncePassLimit {
			j = dtc.JudgmentPass
		}
		if j == dtc.JudgmentFail {
			anyFail = true
		}
		if j == dtc.JudgmentPass {
			anyPass = true
		}
	}
	return j, anyFail, anyPass
}

// Snapshots 从头重放全部历史，计算每个已登记故障码的状态。
func (m *Model) Snapshots() map[int]dtc.Snapshot {
	accs := map[int]*acc{}
	for code, sev := range m.sev {
		accs[code] = &acc{severity: sev}
	}
	slotFree := true
	slotOwner := 0
	var clearBase int64
	var lastOdo int64

	var reports []report
	var samples []sample
	// 本循环各码是否已发生首次失败判定（用于发生次数与冻结帧）
	firstFailDone := map[int]bool{}

	flushCycle := func() {
		// 暖机判定：直接扫描本循环全部采样。
		warmUp := false
		if len(samples) > 0 {
			minT, maxT := samples[0].temp, samples[0].temp
			for _, s := range samples {
				if s.temp < minT {
					minT = s.temp
				}
				if s.temp > maxT {
					maxT = s.temp
				}
			}
			warmUp = maxT-minT >= int64(m.cfg.WarmUpTempRise) && maxT >= int64(m.cfg.WarmUpFinalTemp)
		}
		for code, a := range accs {
			_, anyFail, anyPass := m.judgmentAfter(reports, code)
			completedNoFail := anyPass && !anyFail
			if anyFail {
				a.consecutiveFailCycles++
			} else if completedNoFail {
				a.consecutiveFailCycles = 0
			}
			if a.consecutiveFailCycles >= m.cfg.ConfirmCycles {
				a.confirmed = true
			}
			if anyFail {
				a.faultFreeWarmUps = 0
			} else if warmUp && completedNoFail && (a.confirmed || a.healed) {
				a.faultFreeWarmUps++
			}
			if a.confirmed && a.faultFreeWarmUps >= m.cfg.HealWarmUpCycles {
				a.confirmed = false
				a.pending = false
				a.healed = true
			}
			if a.healed && a.faultFreeWarmUps >= m.cfg.AutoClearWarmUps {
				*a = acc{severity: a.severity}
				if !slotFree && slotOwner == code {
					slotFree = true
					slotOwner = 0
				}
			}
		}
		reports = nil
		samples = nil
		firstFailDone = map[int]bool{}
	}

	for _, ev := range m.events {
		lastOdo = ev.Odometer
		switch ev.Kind {
		case dtc.EventIgnitionOn:
		case dtc.EventEnvironmentSample:
			samples = append(samples, sample{speed: ev.Speed, temp: ev.CoolantTemp})
		case dtc.EventMonitorResult:
			reports = append(reports, report{code: ev.DTCCode, passed: ev.Passed})
			j, _, _ := m.judgmentAfter(reports, ev.DTCCode)
			if j == dtc.JudgmentFail && !firstFailDone[ev.DTCCode] {
				firstFailDone[ev.DTCCode] = true
				a := accs[ev.DTCCode]
				a.occurrences++
				a.pending = true
				if a.healed {
					a.healed = false
					a.consecutiveFailCycles = 0
					a.faultFreeWarmUps = 0
				}
				// 冻结帧：空闲或本人则捕获；他人占用时严重度严格更大才替换。
				if slotFree || slotOwner == ev.DTCCode {
					slotFree = false
					slotOwner = ev.DTCCode
				} else if m.sev[ev.DTCCode] > m.sev[slotOwner] {
					slotOwner = ev.DTCCode
				}
			}
		case dtc.EventIgnitionOff:
			flushCycle()
		case dtc.EventScanToolClear:
			for _, a := range accs {
				*a = acc{severity: a.severity}
			}
			slotFree = true
			slotOwner = 0
			clearBase = ev.Odometer
		}
	}
	out := map[int]dtc.Snapshot{}
	for code, a := range accs {
		j := dtc.JudgmentNone
		if m.ignitionOn {
			j, _, _ = m.judgmentAfter(reports, code)
		}
		out[code] = dtc.Snapshot{
			Code:                  code,
			Pending:               a.pending,
			Confirmed:             a.confirmed,
			Healed:                a.healed,
			JudgedFail:            j == dtc.JudgmentFail,
			Occurrences:           a.occurrences,
			ConsecutiveFailCycles: a.consecutiveFailCycles,
			FaultFreeWarmUps:      a.faultFreeWarmUps,
			OwnsFreezeFrame:       !slotFree && slotOwner == code,
			DistanceSinceClear:    lastOdo - clearBase,
		}
	}
	return out
}
