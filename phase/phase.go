// Package phase 实现单标的的连续交易 / 波动性中断阶段机。
package phase

// State 是交易阶段。
type State int

const (
	Cont State = iota // 连续交易
	Halt              // 波动性中断
)

// Machine 是单标的阶段机。
type Machine struct {
	state     State
	he        int64
	haltCount int64
	extCount  int64
}

// New 建立连续交易阶段机。
func New() *Machine { return &Machine{state: Cont} }

// State 返回当前阶段。
func (m *Machine) State() State { return m.state }

// HaltCount 返回当日中断次数。
func (m *Machine) HaltCount() int64 { return m.haltCount }

// ExtCount 返回本次中断已延长次数。
func (m *Machine) ExtCount() int64 { return m.extCount }

// HE 返回当前中断结束时刻。
func (m *Machine) HE() int64 { return m.he }

// Enter 进入一次中断：结束时刻 he，中断次数加 1。
func (m *Machine) Enter(he int64) {
	m.state = Halt
	m.he = he
	m.extCount = 0
	m.haltCount++
}

// Extend 延长本次中断：更新 he，延长次数加 1。
func (m *Machine) Extend(he int64) {
	m.he = he
	m.extCount++
}

// Resume 恢复连续交易并清零本次中断的延长计数。
func (m *Machine) Resume() {
	m.state = Cont
	m.he = 0
	m.extCount = 0
}
