package fsm

import "fmt"

// observerBuffer is the capacity of each subscription channel.
const observerBuffer = 16

// Fire applies event e to the machine and returns the resulting state.
//
// The transition is atomic with respect to the machine state:
//   - a terminal state absorbs every event with ErrTerminal;
//   - an unknown event fails with ErrNoTransition and has zero side
//     effects (no actions, no log entry, no observer notification);
//   - a self transition (From == To) runs no exit/entry actions but is
//     logged and observed like any other transition;
//   - otherwise the source exit actions run first, then the target
//     entry actions; a failing entry action yields ErrEntryFailed and
//     the machine stays in the source state (exit actions already ran
//     and are not compensated).
func (m *Machine) Fire(e Event) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	from := m.state
	if m.terminals[from] {
		return from, ErrTerminal
	}
	to, ok := m.table[from][e]
	if !ok {
		return from, ErrNoTransition
	}
	if to != from {
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
	m.notify(to)
	return to, nil
}

// notify delivers s to every observer without ever blocking: an
// observer whose buffer is full simply misses this update. A slow
// observer therefore receives an order-preserving subsequence of the
// logged target states, and every received prefix is exact.
func (m *Machine) notify(s State) {
	for _, ch := range m.observers {
		select {
		case ch <- s:
		default:
		}
	}
}

// Log returns a copy of the transitions that have happened so far, in
// order of occurrence. Mutating the result does not affect the machine.
func (m *Machine) Log() []Transition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Transition, len(m.log))
	copy(out, m.log)
	return out
}

// Observe subscribes to state changes and may be called any number of
// times. The returned channel receives the target state of every
// transition committed after the subscription, in order, until its
// buffer fills; further updates are dropped for that observer only
// (see notify). The channel is never closed.
func (m *Machine) Observe() <-chan State {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan State, observerBuffer)
	m.observers = append(m.observers, ch)
	return ch
}
