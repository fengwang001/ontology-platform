package fsm

import "fmt"

// Fire 在当前状态上触发事件 e，返回迁移后的状态。
//
// 语义：
//   - 机器处于终态时，任何事件都返回 ErrTerminal，状态与日志不变；
//   - 当前状态下没有对应转移时返回 ErrNoTransition，零副作用
//     （状态不变、动作不执行、日志不新增、观察者收不到通知）；
//   - 自转移（From == To）合法，但不执行 exit/entry 动作，
//     仍记入日志并通知观察者；
//   - 正常迁移先执行源状态 exit（按注册顺序），再执行目标状态
//     entry（按注册顺序）；entry 返回 error 时，Fire 返回包裹该
//     error 的 ErrEntryFailed，机器停在原状态，日志不新增、观察者
//     收不到通知；已执行的 exit 不会回滚，重试时会再次执行。
func (m *Machine) Fire(e Event) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.terminals[m.state] {
		return m.state, ErrTerminal
	}
	to, ok := m.table[m.state][e]
	if !ok {
		return m.state, ErrNoTransition
	}
	from := m.state
	if from != to {
		for _, f := range m.exits[from] {
			f()
		}
		for _, f := range m.entries[to] {
			if err := f(); err != nil {
				return from, fmt.Errorf("%w: %w", ErrEntryFailed, err)
			}
		}
	}
	m.state = to
	m.log = append(m.log, Transition{From: from, Event: e, To: to})
	for _, ch := range m.observers {
		select {
		case ch <- to:
		default:
			// 观察者通道已满：丢弃本次通知，绝不阻塞 Fire。
		}
	}
	return to, nil
}
