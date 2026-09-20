package fsm

import "fmt"

// Fire 向状态机投递事件 e，并返回迁移后的当前状态。
//
// 拒绝情形（均无任何副作用：状态、Log、观察者均不变）：
//   - 当前为终态：返回 ErrTerminal；
//   - 当前状态没有 e 的转移：返回 ErrNoTransition；
//   - 目标状态 entry 动作失败：返回 fmt.Errorf("%w: %v", ErrEntryFailed, err)。
//
// 成功迁移先执行源状态全部 exit，再执行目标状态全部 entry；
// 自转移（From == To）不执行任何 entry/exit，但仍记 Log、
// 通知观察者一次。
func (m *Machine) Fire(e Event) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.terms[m.state] {
		return m.state, ErrTerminal
	}

	key := struct {
		from  State
		event Event
	}{m.state, e}
	tr, ok := m.trans[key]
	if !ok {
		return m.state, ErrNoTransition
	}

	from := m.state

	if tr.To != from {
		// 先 exit；若随后 entry 失败，本次 exit 不回滚也不补偿，
		// 下次重打同一事件时会重新 exit（恰好再跑一次）。
		m.runExits(from)
		if err := m.runEntries(tr.To); err != nil {
			return from, fmt.Errorf("%w: %w", ErrEntryFailed, err)
		}
	}

	m.state = tr.To
	m.log = append(m.log, tr)
	m.broadcast(tr.To)
	return tr.To, nil
}
