// Package delta defines the change stream events and their shape validation.
package delta

import "errors"

// Op is the kind of change.
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

// ErrInvalidOp is returned for an event whose Op is neither Insert nor Retract.
var ErrInvalidOp = errors.New("delta: invalid event op")

// Valid reports whether the event itself is well-formed. Whether a retraction
// is legal (the value is currently alive) is stateful and checked downstream.
func (e Event) Valid() bool {
	return e.Op == Insert || e.Op == Retract
}
