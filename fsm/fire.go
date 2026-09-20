package fsm

import "fmt"

// observerBuffer is the capacity of each subscription channel returned
// by Observe.
const observerBuffer = 16

// State returns the current state of the machine.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Fire applies event e to the machine and returns the resulting state.
//
// Behavior, in order of checks:
//   - In a terminal state every event fails with ErrTerminal and nothing
//     changes (terminal states are absorbing; their exit actions never
//     run).
//   - If the current state has no transition for e, Fire fails with
//     ErrNoTransition and has zero side effects: no actions run, the log
//     and observers are untouched.
//   - A self transition (From == To) runs neither exit nor entry actions
//     but is still logged and observed.
//   - Otherwise the source exit actions run, then the target entry
//     actions. If an entry action fails, Fire returns its error wrapped
//     in ErrEntryFailed and the machine stays in the source state; the
//     exit actions already ran and are not compensated.
//
// On success the transition is appended to the log and the new state is
// delivered to observers.
func (m *Machine) Fire(e Event) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	from := m.state
	if m.terminals[from] {
		return from, ErrTerminal
	}
	to, ok := m.table[from][e]
	if !ok {
		return from, fmt.Errorf("%w: %q in state %q", ErrNoTransition, e, from)
	}
	if to != from {
		for _, f := range m.exit[from] {
			f()
		}
		for _, f := range m.entry[to] {
			if err := f(); err != nil {
				return from, fmt.Errorf("%w: %w", ErrEntryFailed, err)
			}
		}
	}
	m.state = to
	m.log = append(m.log, Transition{From: from, Event: e, To: to})
	m.notify(to)
	return to, nil
}

// Log returns the transitions that have succeeded so far, in order of
// occurrence. The returned slice is a copy; mutating it does not affect
// the machine.
func (m *Machine) Log() []Transition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Transition, len(m.log))
	copy(out, m.log)
	return out
}

// Observe subscribes to state changes and may be called any number of
// times. Each successful transition sends its target state to every
// subscriber, so the i-th value received on the channel equals the To
// field of the i-th logged transition.
//
// Delivery is non-blocking: if a subscriber's channel buffer
// (observerBuffer) is full, that state is dropped for that subscriber
// only, so a slow observer can never stall Fire. Because drops only
// remove elements, the values a subscriber actually receives always form
// an in-order subsequence of the logged To states; a subscriber that
// keeps up receives the full sequence. The channel is never closed.
func (m *Machine) Observe() <-chan State {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan State, observerBuffer)
	m.observers = append(m.observers, ch)
	return ch
}

// notify delivers s to every observer without blocking. Callers must
// hold m.mu.
func (m *Machine) notify(s State) {
	for _, ch := range m.observers {
		select {
		case ch <- s:
		default:
		}
	}
}
