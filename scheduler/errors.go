// Package scheduler provides a reservation-with-backfill job scheduler.
package scheduler

import "errors"

// Distinguishable rejection reasons. Callers may compare with errors.Is.
var (
	// ErrInvalidNodes is returned when the cluster node count is not positive.
	ErrInvalidNodes = errors.New("scheduler: node count must be positive")
	// ErrInvalidJobNodes is returned when a job requests a non-positive
	// node count or more nodes than the cluster has.
	ErrInvalidJobNodes = errors.New("scheduler: job nodes must be in [1,N]")
	// ErrInvalidDuration is returned when a job declares a non-positive
	// estimated duration.
	ErrInvalidDuration = errors.New("scheduler: estimated duration must be positive")
	// ErrDuplicateID is returned when a submitted job identifier is already
	// known to the scheduler (queued, running, or historically finished).
	ErrDuplicateID = errors.New("scheduler: duplicate job identifier")
	// ErrNotRunning is returned when an attempt is made to finish a job that
	// is not currently running.
	ErrNotRunning = errors.New("scheduler: job is not running")
	// ErrClockRollback is returned when Advance receives a time earlier than
	// the scheduler's current time.
	ErrClockRollback = errors.New("scheduler: clock cannot move backwards")
)
