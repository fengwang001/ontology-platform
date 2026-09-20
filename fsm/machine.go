package fsm

import (
	"fmt"
	"sync"
)

// observerBuffer is the capacity of each observer channel. Fire never
// blocks on a slow observer: when an observer's channel is full the
// overflowing notification is dropped (see Observe for the contract).
const observerBuffer = 64

type tableKey struct {
	state State
	event Event
}

// Machine is a session protocol state machine. A Machine is safe for
// concurrent use: all mutating operations are serialized internally.
type Machine struct {
	mu        sync.Mutex
	current   State
	terminals map[State]struct{}
	table     map[tableKey]State
	entries   map[State][]func() error
	exits     map[State][]func()
	log       []Transition
	observers map[int]chan State
	nextObsID int
}

// New builds a machine starting in initial, with the given terminal states
// and transition table. It fails when:
//   - the same (From, Event) pair appears more than once in table, or
//   - initial never appears as a From or To of any transition.
//
// A terminal state that is never referenced by the table is accepted.
func New(initial State, terminals []State, table []Transition) (*Machine, error) {
	transitions := make(map[tableKey]State, len(table))
	referenced := false

	for _, t := range table {
		key := tableKey{state: t.From, event: t.Event}
		if _, exists := transitions[key]; exists {
			return nil, fmt.Errorf(
				"fsm: duplicate transition for (%s, %s)", t.From, t.Event)
		}
		transitions[key] = t.To
		if t.From == initial || t.To == initial {
			referenced = true
		}
	}

	if !referenced {
		return nil, fmt.Errorf("fsm: initial state %q is not present in the transition table", initial)
	}

	termSet := make(map[State]struct{}, len(terminals))
	for _, s := range terminals {
		termSet[s] = struct{}{}
	}

	return &Machine{
		current:   initial,
		terminals: termSet,
		table:     transitions,
		entries:   make(map[State][]func() error),
		exits:     make(map[State][]func()),
		observers: make(map[int]chan State),
	}, nil
}
