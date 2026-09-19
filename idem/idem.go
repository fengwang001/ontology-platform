// Package idem provides an idempotency-key executor: the first call with a
// given key runs the function, later calls with the same key and fingerprint
// replay the cached result, and concurrent calls single-flight.
package idem

import "errors"

// Result is the value produced by executing the user function.
type Result struct {
	Code int
	Body string
}

// Outcome describes how a Do call was served.
type Outcome int

const (
	// Executed means this call actually ran the function.
	Executed Outcome = iota
	// Replayed means a previously completed record was returned.
	Replayed
	// Waited means this call blocked on an in-flight call with the same
	// key and fingerprint, then received that call's result.
	Waited
)

// String returns a human-readable name for the outcome.
func (o Outcome) String() string {
	switch o {
	case Executed:
		return "Executed"
	case Replayed:
		return "Replayed"
	case Waited:
		return "Waited"
	}
	return "Unknown"
}

// ErrFingerprintMismatch is returned when a key is reused with a different
// request fingerprint. The stored record is never overwritten in this case.
var ErrFingerprintMismatch = errors.New("idem: fingerprint mismatch")

// retriableError marks an infrastructure-class failure that must not be
// cached.
type retriableError struct{ err error }

func (e retriableError) Error() string { return e.err.Error() }
func (e retriableError) Unwrap() error { return e.err }

// Retriable wraps err to mark it as an infrastructure-class failure. A
// function failing with a retriable error is never cached and its slot is
// released immediately, so the next call with the same key re-executes.
func Retriable(err error) error {
	if err == nil {
		return nil
	}
	return retriableError{err: err}
}

// isRetriable reports whether err carries the retriable marker.
func isRetriable(err error) bool {
	var r retriableError
	return errors.As(err, &r)
}
