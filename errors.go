package rebalance

import "errors"

var (
	ErrInvalidPartitionCount = errors.New("invalid target partition count")
	ErrRebalanceInProgress   = errors.New("rebalance already in progress")
	ErrNotMigrating          = errors.New("no rebalance in progress")
	ErrMigrationIncomplete   = errors.New("migration not complete")
	ErrNewKeyWhileMigrating  = errors.New("new keys cannot be written during migration")

	errInvariantViolated = errors.New("migration invariant violated: record missing from old partition")
	ErrInvalidSnapshot   = errors.New("invalid migration snapshot")
)
