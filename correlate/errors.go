// Package correlate matches out-of-order responses to in-flight requests.
package correlate

import "errors"

// Delivery failures. The three orphan kinds are pairwise distinguishable.
var (
	// ErrUnknownID: the ID was never allocated.
	ErrUnknownID = errors.New("correlate: id never allocated")
	// ErrIdleID: the prior request using the slot already settled and the
	// slot has not been reallocated yet.
	ErrIdleID = errors.New("correlate: id is idle, previous request settled")
	// ErrStaleID: the slot is currently held by a newer request generation;
	// this response is a late arrival from an older generation.
	ErrStaleID = errors.New("correlate: stale response for a newer request")
)

// Request / cancel failures.
var (
	// ErrCapacity means the hard in-flight limit was reached.
	ErrCapacity = errors.New("correlate: in-flight capacity reached")
	// ErrBadBudget means a non-positive timeout budget was supplied.
	ErrBadBudget = errors.New("correlate: timeout budget must be positive")
	// ErrNotInFlight means no pending request matches the cancel request.
	ErrNotInFlight = errors.New("correlate: no in-flight request for id")
)
