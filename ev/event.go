// Package ev defines change-stream events, their validation and the
// inverse-event relation. It depends on no other project package.
package ev

import (
	"errors"
	"fmt"
)

// ErrInvalidEvent is the sentinel for an empty Key or an unknown Op.
var ErrInvalidEvent = errors.New("ev: invalid event")

// Op identifies the kind of change.
type Op uint8

const (
	Insert  Op = iota + 1 // one copy of Val becomes live at Key
	Retract               // one live copy of Val is withdrawn at Key
)

// Event is one change on group Key: one copy of Val is inserted or retracted.
type Event struct {
	Key string
	Val int64
	Op  Op
}

// Validate returns an ErrInvalidEvent-wrapped error when e carries an empty
// Key or an unknown Op; otherwise nil.
func (e Event) Validate() error {
	if e.Key == "" || (e.Op != Insert && e.Op != Retract) {
		return fmt.Errorf("%w: key=%q op=%d", ErrInvalidEvent, e.Key, e.Op)
	}
	return nil
}

// Inverse reports whether a and b are opposite changes on the same Key/Val.
// The relation is symmetric; callers that need only one cancellation
// direction must additionally check the operation order.
func Inverse(a, b Event) bool {
	return a.Key == b.Key && a.Val == b.Val &&
		((a.Op == Insert && b.Op == Retract) ||
			(a.Op == Retract && b.Op == Insert))
}
