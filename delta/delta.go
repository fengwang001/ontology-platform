// Package delta defines the change stream: a value inserted into or
// retracted from a group key, plus stateless legality checks.
package delta

import "errors"

// Op is the kind of a change event.
type Op uint8

const (
	Insert  Op = iota // a value becomes alive in the group
	Retract           // a previously inserted value ceases to be alive
)

// Event is one change on group Key. Val is the value whose alive status flips.
type Event struct {
	Key string
	Val int64
	Op  Op
}

// ErrInvalidEvent is returned when an event's Op is not Insert or Retract.
var ErrInvalidEvent = errors.New("delta: invalid event operation")

// ErrRetractMissing is returned when a retraction has no matching live value.
// It is state-dependent, so the agg package detects it, but the sentinel
// lives here because it is part of the delta contract.
var ErrRetractMissing = errors.New("delta: retract of a value that was never inserted")

// Valid reports whether the event itself is well-formed, independent of any
// view state: Op must be one of the two known operations.
func (e Event) Valid() bool {
	return e.Op == Insert || e.Op == Retract
}
