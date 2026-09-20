package fsm

import "sync"

// Machine 是一台并发安全的会话协议状态机。
type Machine struct {
	mu        sync.Mutex
	current   State
	terminals map[State]struct{}
	table     map[stateKey]State
	entries   map[State][]entryFunc
	exits     map[State][]exitFunc
	log       []Transition
	observers map[chan State]struct{}
}

// stateKey 是转移表的复合键：当前状态 + 事件。
type stateKey struct {
	from  State
	event Event
}

// New 创建状态机。
// OnEntry 注册进入状态 s 时执行的动作。
// 同一状态上可多次注册，迁移成功时按注册顺序全部执行。
func (m *Machine) OnEntry(s State, f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[s] = append(m.entries[s], f)
}

// OnExit 注册离开状态 s 时执行的动作。
// 同一状态上可多次注册，迁移成功时按注册顺序全部执行。
func (m *Machine) OnExit(s State, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exits[s] = append(m.exits[s], f)
}

// State 返回当前状态。
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Log 返回已发生转移的副本，按发生顺序排列。
// 每次调用返回独立切片，调用方修改不影响内部状态。
func (m *Machine) Log() []Transition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Transition, len(m.log))
	copy(out, m.log)
	return out
}

// Observe 订阅状态变更，可多次调用。
// 每个订阅者拿到独立的通道，投递语义见 notifyObservers 的注释。
func (m *Machine) Observe() <-chan State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.addObserver()
}
