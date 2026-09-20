package fsm

// State identifies a state of the session protocol machine.
type State string

// Event identifies an input fed to the machine.
type Event string

// Transition declares that Event in state From moves the machine to To.
type Transition struct {
	From  State
	Event Event
	To    State
}

var (
	// ErrNoTransition is returned when the current state has no transition
	// for the fired event.
	ErrNoTransition = newFSMError("fsm: no transition for event in state")

	// ErrTerminal is returned when an event is fired while the machine is
	// already in a terminal state.
	ErrTerminal = newFSMError("fsm: machine is in a terminal state")

	// ErrEntryFailed wraps the error returned by an entry action.
	ErrEntryFailed = newFSMError("fsm: entry action failed")
)

type fsmError string

func (e fsmError) Error() string { return string(e) }
