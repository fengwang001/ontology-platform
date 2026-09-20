package fsm

// OnEntry registers an action executed every time the machine enters
// state s. Multiple actions on the same state run in registration order,
// after the source state's exit actions. A non-nil error aborts the
// transition: Fire returns it wrapped in ErrEntryFailed and the machine
// stays in the source state.
//
// Actions run while the machine lock is held; they must not call back
// into the Machine.
func (m *Machine) OnEntry(s State, f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entry[s] = append(m.entry[s], f)
}

// OnExit registers an action executed every time the machine leaves
// state s. Multiple actions on the same state run in registration order,
// before the target state's entry actions. Exit actions cannot fail.
//
// Actions run while the machine lock is held; they must not call back
// into the Machine.
func (m *Machine) OnExit(s State, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exit[s] = append(m.exit[s], f)
}
