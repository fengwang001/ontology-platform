// Package ev defines change-stream events, their validation and the
// inverse-event relation. It depends on no other package.
package ev

import "errors"

// Op is the operation carried by an event.
type Op uint8

const (
	OpInsert  Op = 1
	OpRetract Op = 2
)

// Event is one change on group Key: value Val is inserted or retracted.
type Event struct {
	Key string
	Val int64
	Op  Op
}

// ErrInvalidEvent is the sentinel for an empty Key or an unknown Op.
var ErrInvalidEvent = errors.New("ev: invalid event")

// Valid reports whether e is well-formed: non-empty key and known op.
func (e Event) Valid() bool {
	return e.Key != "" && (e.Op == OpInsert || e.Op == OpRetract)
}

// Validate returns ErrInvalidEvent for a malformed event, nil otherwise.
func Validate(e Event) error {
	if !e.Valid() {
		return ErrInvalidEvent
	}
	return nil
}

// AreInverse reports whether a and b undo each other: same key and value,
// opposite (and both known) ops. The relation is symmetric.
func AreInverse(a, b Event) bool {
	if a.Key != b.Key || a.Val != b.Val {
		return false
	}
	switch {
	case a.Op == OpInsert && b.Op == OpRetract:
		return true
	case a.Op == OpRetract && b.Op == OpInsert:
		return true
	default:
		return false
	}
}
