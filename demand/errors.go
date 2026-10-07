// Package demand implements a maximum-demand controller for industrial users.
//
// The package is split into cooperating modules by responsibility:
//   - config.go      configuration and load-spec validation
//   - errors.go      distinguishable error categories
//   - history.go     sliding-window energy ledger (constant memory)
//   - peak.go        measured peak-demand tracking
//   - loads.go       controllable-load registry (state, lock, timers)
//   - predict.go     violation prediction and cut-set selection
//   - controller.go  the controller tying everything together, concurrency-safe
package demand

import "errors"

// Distinguishable error categories; callers test with errors.Is.
//
// Report validation order: ErrInvalidParam > ErrDataIllegal > ErrTimeRegression.
// Maintenance-op order:    ErrInvalidParam > ErrLoadNotFound > ErrStateNotAllowed.
var (
	// ErrInvalidParam reports invalid parameters (config, load spec,
	// negative report energy, negative timestamp, ...).
	ErrInvalidParam = errors.New("demand: invalid parameter")
	// ErrDataIllegal reports illegal meter data (interval average power
	// above the configured physical limit; this includes a regressed
	// timestamp carrying positive energy, i.e. infinite average power).
	ErrDataIllegal = errors.New("demand: illegal meter data")
	// ErrTimeRegression reports a timestamp not strictly greater than
	// the last accepted one.
	ErrTimeRegression = errors.New("demand: timestamp regression")
	// ErrLoadNotFound reports an unknown load ID.
	ErrLoadNotFound = errors.New("demand: load not found")
	// ErrStateNotAllowed reports an operation not allowed in the current
	// state (duplicate add, repeated lock/unlock, deleting an existing load).
	ErrStateNotAllowed = errors.New("demand: state not allowed")
)
