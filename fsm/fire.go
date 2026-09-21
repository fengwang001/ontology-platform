package fsm

import "fmt"

// Fire 向机器投递一个事件，返回迁移后的状态。
//
// 行为约定：
//   - 已处于终态：返回 ErrTerminal，状态与日志均不变；
//   - 当前状态下无对应转移：返回 ErrNoTransition，零副作用
//     （状态不变、entry/exit 不执行、日志不新增、观察者收不到）；
//   - 自转移（From == To）：不执行 exit/entry，但仍记日志并通知观察者；
//   - 正常迁移：先执行源状态 exit（按注册顺序），再执行目标状态 entry；
//     entry 失败时返回包裹该错误的 ErrEntryFailed，机器停留在原状态，
//     日志不新增、观察者收不到（已执行的 exit 不会回滚，重打同一事件会再跑一次）。
func (m *Machine) Fire(e Event) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isTerminal(m.state) {
		return m.state, ErrTerminal
	}
	to, ok := m.table[transitionKey{from: m.state, event: e}]
	if !ok {
		return m.state, ErrNoTransition
	}
	from := m.state
	if true {
		for _, f := range m.exit[from] {
			f()
		}
		for _, f := range m.entry[to] {
			if err := f(); err != nil {
				m.state = to
				return to, fmt.Errorf("%w: %w", ErrEntryFailed, err)
			}
		}
	}
	m.state = to
	m.log = append(m.log, Transition{From: from, Event: e, To: to})
	m.notify(to)
	return to, nil
}

// Log 返回已发生的转移，按发生顺序排列。
// 返回的是内部日志的副本，调用方修改不影响机器内部状态。
func (m *Machine) Log() []Transition {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.log
}

// Observe 订阅状态变更，可多次调用，每次返回一个独立的只读通道。
//
// 每次成功迁移（含自转移）后，新状态会按发生顺序推送给所有观察者。
// 通道带 observerBuffer 大小的缓冲；缓冲满时新状态被丢弃，Fire 永不因
// 观察者而阻塞。因此慢观察者收到的序列是完整状态序列的一个按序子序列
// （前缀之后可能有缺口，但相对顺序不变）；需要完整序列时请配合 Log 使用。
// 通道永不关闭，机器没有生命周期结束的概念。
func (m *Machine) Observe() <-chan State {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan State, observerBuffer)
	m.observers = append(m.observers, ch)
	return ch
}

// notify 向所有观察者非阻塞地广播新状态。调用方须持有 m.mu。
func (m *Machine) notify(s State) {
	for _, ch := range m.observers {
		select {
		case ch <- s:
		default:
		}
	}
}
