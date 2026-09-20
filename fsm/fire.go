package fsm

import "fmt"

// Fire 向状态机投递事件 e。
//
// 返回迁移后的状态与可能的错误，错误可用 errors.Is 判定：
//   - ErrTerminal：已在终态，状态与 Log 均不变；
//   - ErrNoTransition：当前状态不接受该事件。此情况下保证零副作用：
//     状态不变、不执行任何 entry/exit、Log 不新增、观察者无感知；
//   - ErrEntryFailed：目标状态的 entry 动作失败（错误同时包裹了
//     动作返回的原始错误）。机器停留在源状态，Log 不新增，观察者
//     无感知；源状态的 exit 已经执行过一次且不会回滚，下次成功
//     迁移时会再执行一次。
//
// 自转移（From == To）合法但不触发 exit/entry，仍计入 Log 并通知
// 观察者。
func (m *Machine) Fire(e Event) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.terminals[m.current]; ok {
		return m.current, ErrTerminal
	}

	to, ok := m.table[stateKey{from: m.current, event: e}]
	if !ok {
		return m.current, ErrNoTransition
	}

	from := m.current
	if from != to {
		runExits(m.exits[from])
		if err := runEntries(m.entries[to]); err != nil {
			return from, fmt.Errorf("%w (state %q): %w",
				ErrEntryFailed, to, err)
		}
	}

	m.current = to
	m.log = append(m.log, Transition{From: from, Event: e, To: to})
	m.notifyObservers(to)
	return to, nil
}
