// Package evt defines the timestamped event type, its validation and the sole
// clustering predicate of the session-window model: two points belong together
// iff their time distance does not exceed gap. It has no dependencies.
package evt

import "errors"

// ErrInvalidEvent is returned when an event fails validation.
var ErrInvalidEvent = errors.New("evt: invalid event: key must not be empty")

// Event is an upstream timestamped occurrence. TS uses an arbitrary epoch;
// only ordering and distances between timestamps matter.
type Event struct {
	Key string
	TS  int64
}

// Valid reports whether the event is admissible.
func (e Event) Valid() error {
	if e.Key == "" {
		return ErrInvalidEvent
	}
	return nil
}

// Mergeable is the unique session predicate: points a and b stay in the same
// session exactly when |a-b| <= gap. Callers must pass gap > 0; a non-positive
// gap is rejected at api construction time, so this function never sees one.
// The subtraction is ordered to avoid int64 overflow at extreme timestamps.
func Mergeable(a, b, gap int64) bool {
	d := a - b
	if d < 0 {
		d = b - a // if this is also negative, the distance overflowed int64
	}
	return d >= 0 && d <= gap
}
