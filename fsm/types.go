// Package fsm implements a small, concurrency-safe session protocol
// state machine.
//
// The machine is driven by events: for each (current state, event) pair a
// transition table names the next state. Optional entry/exit actions are
// executed around successful transitions.
package fsm

import "errors"

// State is a named state of the session protocol.
type State string

// Event is an external stimulus that may trigger a transition.
type Event string

// Transition declares that firing Event while in state From moves the
// machine to state To. From and To may be equal; such a self-transition is
// recorded and observed like any other transition but does not run the
// state's exit/entry actions.
type Transition struct {
	From  State
	Event Event
	To    State
}

// Rejection reasons. Every error returned by Fire wraps exactly one of
// these, so callers can use errors.Is to distinguish the cause.
var (
	// ErrNoTransition means the event has no transition from the current
	// state. Such a Fire has no side effects at all.
	ErrNoTransition = errors.New("fsm: no transition for event in state")

	// ErrTerminal means the machine has reached a terminal state. Events
	// are absorbed in terminal states: state and log never change again.
	ErrTerminal = errors.New("fsm: machine is in a terminal state")

	// ErrEntryFailed wraps the error returned by the destination state's
	// entry action. When it occurs the machine stays in the source state
	// and nothing is logged or observed.
	ErrEntryFailed = errors.New("fsm: entry action failed")
)
