package temporal

import "fmt"

// TraversalErrorCode enumerates the mutually exclusive failure classes that a
// traversal request may report. When several conditions hold simultaneously
// the traversal engine reports exactly one, in this documented priority:
//
//	ErrBeforeHorizon > ErrStartNotFound > ErrLimitExceeded > ErrMissingHistory
//	(except that start-existence is checked first when it can be answered,
//	 see traverse.go for the precise deterministic rule).
type TraversalErrorCode int

const (
	// ErrStartNotFound: the start object did not exist at the fixed instant.
	ErrStartNotFound TraversalErrorCode = iota + 1
	// ErrBeforeHorizon: the fixed instant is earlier than the oldest instant
	// the system can replay.
	ErrBeforeHorizon
	// ErrLimitExceeded: depth or number of visited objects exceeds the
	// pre-declared limits.
	ErrLimitExceeded
	// ErrMissingHistory: underlying historical data needed to decide one
	// traversal step is missing/corrupt.
	ErrMissingHistory
)

// TraversalError reports a single classified failure. A traversal that fails
// returns no partial snapshot and mutates no history.
type TraversalError struct {
	Code   TraversalErrorCode
	At     Instant // snapshot instant requested
	Detail string
}

func (e *TraversalError) Error() string {
	return fmt.Sprintf("temporal traversal error: code=%d at=%d: %s", e.Code, e.At, e.Detail)
}
