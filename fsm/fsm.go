// Package fsm implements a session-protocol finite state machine.
//
// A Machine is built from an initial state, a set of terminal states and
// a transition table. Events drive transitions; states may carry entry
// and exit actions. The machine is safe for concurrent use.
package fsm

import (
	"errors"
	"fmt"
	"sync"
)

// State identifies a node of the state machine.
type State string

// Event identifies an input that may trigger a transition.
type Event string

// Transition describes a single edge: when Event fires while the machine
// is in From, the machine moves to To.
type Transition struct {
	From  State
	Event Event
	To    State
}

var (
	// ErrNoTransition is returned by Fire when the current state has no
	// transition for the fired event. Use errors.Is to test for it.
	ErrNoTransition = errors.New("fsm: no transition for event in current state")
	// ErrTerminal is returned by Fire when the machine already sits in a
	// terminal state. Terminal states absorb every event.
	ErrTerminal = errors.New("fsm: machine is in a terminal state")
	// ErrEntryFailed wraps the error returned by a failed entry action.
	// The machine stays in the source state when this happens.
	ErrEntryFailed = errors.New("fsm: entry action failed")
)

// Machine is a finite state machine. The zero value is not usable;
// construct one with New.
type Machine struct {
	mu        sync.Mutex
	state     State
	terminals map[State]bool
	table     map[State]map[Event]State
	entry     map[State][]func() error
	exit      map[State][]func()
	log       []Transition
	observers []chan State
}

// New builds a Machine. initial is the starting state, terminals the set
// of absorbing states, and table the full transition table.
//
// New returns an error if the table contains two transitions with the
// same (From, Event) pair, or if initial does not appear as the From or
// To of any table entry. Terminal states that never appear in the table
// are allowed.
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	m := &Machine{
		state:     initial,
		terminals: make(map[State]bool, len(terminals)),
		table:     make(map[State]map[Event]State, len(table)),
		entry:     make(map[State][]func() error),
		exit:      make(map[State][]func()),
	}
	for _, t := range terminals {
		m.terminals[t] = true
	}
	seen := make(map[State]bool, len(table))
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
		seen[tr.From] = true
		seen[tr.To] = true
	}
	if !seen[initial] {
		return nil, fmt.Errorf("fsm: initial state %q does not appear in the transition table", initial)
	}
	return m, nil
}
