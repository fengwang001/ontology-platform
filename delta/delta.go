// Package delta defines the change stream and validates individual events.
// It must not depend on any other package of this module.
package delta

import "errors"

// Op is the kind of a change.
type Op uint8

const (
	Insert  Op = iota + 1 // a value becomes live
	Retract               // a previously inserted value is withdrawn
)

// Event is one change: Val under Key is inserted or retracted.
type Event struct {
	Key string
	Val int64
	Op  Op
}

// ErrInvalidEvent is returned for a malformed event (unknown op, empty key).
var ErrInvalidEvent = errors.New("delta: invalid event")

// Valid reports whether the event itself is well-formed. It deliberately does
// NOT decide whether a retraction has a matching live value: that is a stateful
// check owned by the agg package.
func (ev Event) Valid() bool {
	if ev.Key == "" {
		return false
	}
	return ev.Op == Insert || ev.Op == Retract
}

// Check applies Valid and returns the sentinel error for use in guards.
func (ev Event) Check() error {
	if !ev.Valid() {
		return ErrInvalidEvent
	}
	return nil
}
