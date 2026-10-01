package plancache

import "errors"

// Distinguishable rejection reasons. Operations report the first applicable
// cause in the order documented on Selector.
var (
	// ErrKTooSmall: New received K < 1.
	ErrKTooSmall = errors.New("plancache: custom trial count K must be at least 1")
	// ErrPNegative: New received a negative planning overhead P.
	ErrPNegative = errors.New("plancache: planning overhead P must be non-negative")
	// ErrEmptyName: Prepare received an empty statement name.
	ErrEmptyName = errors.New("plancache: statement name must not be empty")
	// ErrNameExists: Prepare received a name already registered.
	ErrNameExists = errors.New("plancache: statement already exists")
	// ErrStatementNotFound: no statement is registered under the name.
	ErrStatementNotFound = errors.New("plancache: statement not found")
	// ErrPendingExists: Next called while a decision is still pending.
	ErrPendingExists = errors.New("plancache: a decision is already pending")
	// ErrPendingMismatch: no pending decision or the pending decision kind
	// does not match the reporting call.
	ErrPendingMismatch = errors.New("plancache: pending decision missing or of wrong kind")
	// ErrCostOutOfRange: cost is outside [0, 2^40].
	ErrCostOutOfRange = errors.New("plancache: cost must be within [0, 2^40]")
	// ErrVersionNotGreater: Bump received a version not greater than the
	// current architecture version.
	ErrVersionNotGreater = errors.New("plancache: new architecture version must be greater than the current one")
)
