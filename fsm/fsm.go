package fsm

import (
	"fmt"
	"sync"
)

// Machine is a session-protocol finite state machine.
//
// All methods are safe for concurrent use. Actions registered with
// OnEntry/OnExit run while the machine lock is held, so they must be
// fast and must not call back into the machine.
type Machine struct {
	mu        sync.Mutex
	state     State
	terminals map[State]bool
	table     map[State]map[Event]State
	entries   map[State][]func() error
	exits     map[State][]func()
	log       []Transition
	observers []chan State
}

// New builds a Machine starting at initial. terminals lists the
// absorbing states; a terminal state that never appears in table is
// legal. table is the transition table.
//
// New fails when the same (From, Event) pair appears twice in table,
// or when initial does not appear as any From or To in table.
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	m := &Machine{
		state:     initial,
		terminals: make(map[State]bool, len(terminals)),
		table:     make(map[State]map[Event]State, len(table)),
		entries:   make(map[State][]func() error),
		exits:     make(map[State][]func()),
	}
	for _, s := range terminals {
		m.terminals[s] = true
	}
	referenced := make(map[State]bool, len(table))
	for _, tr := range table {
		byEvent, ok := m.table[tr.From]
		if !ok {
			byEvent = make(map[Event]State)
			m.table[tr.From] = byEvent
		}
		if _, dup := byEvent[tr.Event]; dup {
			return nil, fmt.Errorf("fsm: duplicate transition for (%q, %q)", tr.From, tr.Event)
		}
		byEvent[tr.Event] = tr.To
		referenced[tr.From] = true
		referenced[tr.To] = true
	}
	if !referenced[initial] {
		return nil, fmt.Errorf("fsm: initial state %q not referenced by the transition table", initial)
	}
	return m, nil
}

// OnEntry registers f to run whenever the machine enters state s.
// Multiple functions on the same state run in registration order.
// A non-nil error aborts the transition (see ErrEntryFailed).
func (m *Machine) OnEntry(s State, f func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[s] = append(m.entries[s], f)
}

// OnExit registers f to run whenever the machine leaves state s.
// Multiple functions on the same state run in registration order.
func (m *Machine) OnExit(s State, f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exits[s] = append(m.exits[s], f)
}

// State reports the current state.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}
