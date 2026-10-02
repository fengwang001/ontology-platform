package canfault

import "fmt"

// naiveOp 是朴素模拟器的一步操作。
type naiveOp struct {
	kind string // "apply" | "restart" | "idle11"
	ev   Event
}

// naiveSnapshot 是朴素模拟器的状态视图。
type naiveSnapshot struct {
	tec         int
	rec         int
	state       State
	recovering  bool
	idleCount   int
	opSeq       int
	transitions []Transition
}

// naiveModel 是按需求逐字实现的独立朴素参考模型，
// 与 Controller 共用类型定义，但不依赖其任何方法。
type naiveModel struct {
	tec, rec    int
	recovering  bool
	idleCount   int
	opSeq       int
	transitions []Transition
}

func newNaiveModel() *naiveModel {
	return &naiveModel{transitions: make([]Transition, 0)}
}

func (m *naiveModel) state() State {
	switch {
	case m.tec > 255:
		return BusOff
	case m.tec > 127 || m.rec > 127:
		return ErrorPassive
	default:
		return ErrorActive
	}
}

func (m *naiveModel) apply(ev Event) error {
	if ev < TxOK || ev > RxErrDominant {
		return ErrInvalidEvent
	}
	before := m.state()
	if before == BusOff {
		return ErrApplyWhileBusOff
	}
	switch ev {
	case TxOK:
		if m.tec > 0 {
			m.tec--
		}
	case TxErr:
		m.tec += 8
	case TxAckErr:
		if before == ErrorActive {
			m.tec += 8
		}
	case RxOK:
		if m.rec > 127 {
			m.rec = 127
		} else if m.rec > 0 {
			m.rec--
		}
	case RxErr:
		m.rec++
	case RxErrDominant:
		m.rec += 8
	}
	m.opSeq++
	if after := m.state(); after != before {
		m.transitions = append(m.transitions, Transition{OpSeq: m.opSeq, Old: before, New: after})
	}
	return nil
}

func (m *naiveModel) restart() error {
	if m.state() != BusOff {
		return ErrRestartNotBusOff
	}
	if m.recovering {
		return ErrRestartAlreadyRecovering
	}
	m.recovering = true
	m.idleCount = 0
	m.opSeq++
	return nil
}

func (m *naiveModel) idle11() error {
	if !m.recovering {
		return ErrIdle11NotRecovering
	}
	before := m.state()
	m.idleCount++
	if m.idleCount == 128 {
		m.tec = 0
		m.rec = 0
		m.recovering = false
		m.idleCount = 0
	}
	m.opSeq++
	if after := m.state(); after != before {
		m.transitions = append(m.transitions, Transition{OpSeq: m.opSeq, Old: before, New: after})
	}
	return nil
}

func (m *naiveModel) snapshot() naiveSnapshot {
	ts := make([]Transition, len(m.transitions))
	copy(ts, m.transitions)
	return naiveSnapshot{
		tec: m.tec, rec: m.rec, state: m.state(),
		recovering: m.recovering, idleCount: m.idleCount,
		opSeq: m.opSeq, transitions: ts,
	}
}

func (m *naiveModel) step(op naiveOp) error {
	switch op.kind {
	case "apply":
		return m.apply(op.ev)
	case "restart":
		return m.restart()
	case "idle11":
		return m.idle11()
	default:
		panic(fmt.Sprintf("naive: unknown op kind %q", op.kind))
	}
}
