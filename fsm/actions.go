package fsm

// OnEntry 为状态 s 注册一个 entry 动作。
// 可对同一状态多次调用；触发时按注册顺序全部执行。
// 若任意一个 entry 动作返回错误，后续 entry 动作不再执行，
// Fire 返回包裹该错误的 ErrEntryFailed，机器停留在原状态。
func (m *Machine) OnEntry(s State, f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[s] = append(m.entries[s], f)
}

// OnExit 为状态 s 注册一个 exit 动作。
// 可对同一状态多次调用；触发时按注册顺序全部执行。
func (m *Machine) OnExit(s State, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exits[s] = append(m.exits[s], f)
}

// runEntries 依次执行状态 s 的 entry 动作，返回第一个错误。
// 必须在持有 m.mu 时调用（动作执行期间全程持锁，保证
// “失败后整体不迁移”对并发 Fire 也成立）。
func (m *Machine) runEntries(s State) error {
	for _, f := range m.entries[s] {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

// runExits 依次执行状态 s 的 exit 动作。必须在持有 m.mu 时调用。
func (m *Machine) runExits(s State) {
	for _, f := range m.exits[s] {
		f()
	}
}
