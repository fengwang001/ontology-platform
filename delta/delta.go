// Package delta defines the change stream and validates individual events.
package delta

import "errors"

// Op identifies the kind of change applied to one value within a group.
type Op uint8

const (
	Insert  Op = iota + 1 // a value becomes alive
	Retract               // a previously inserted value is withdrawn
)

// Event is one change: value Val under group key Key is inserted or retracted.
type Event struct {
	Key string
	Val int64
	Op  Op
}

// ErrBadOp is returned when an event carries neither Insert nor Retract.
var ErrBadOp = errors.New("delta: unknown operation")

// Valid reports whether the event itself is well-formed. It does not (and
// cannot) decide whether a Retract matches a live value; that is stateful.
func (e Event) Valid() bool {
	return e.Op == Insert || e.Op == Retract
}

// Validate returns a sentinel error for a malformed event.
func (e Event) Validate() error {
	if !e.Valid() {
		return ErrBadOp
	}
	return nil
}
