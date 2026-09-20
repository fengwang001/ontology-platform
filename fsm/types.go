// Package fsm implements a session-protocol finite state machine.
//
// A Machine is driven by events: Fire looks up the transition table for
// the current state, runs exit/entry actions, records the transition in
// an internal log and notifies observers. The zero dependency design
// keeps the package usable from any protocol layer.
package fsm

import "errors"

// State identifies a node of the state machine.
type State string

// Event identifies an input that may trigger a transition.
type Event string

// Transition describes a single edge of the state machine graph.
type Transition struct {
	From  State
	Event Event
	To    State
}

// Rejection reasons returned by Fire. Use errors.Is to test for them.
var (
	// ErrNoTransition is returned when the current state has no
	// transition registered for the fired event.
	ErrNoTransition = errors.New("fsm: no transition for event in current state")
	// ErrTerminal is returned when the machine already sits in a
	// terminal state and therefore absorbs every further event.
	ErrTerminal = errors.New("fsm: machine is in a terminal state")
	// ErrEntryFailed is returned when an entry action of the target
	// state fails; the machine stays in the source state.
	ErrEntryFailed = errors.New("fsm: entry action failed")
)
