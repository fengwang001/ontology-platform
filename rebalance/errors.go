package rebalance

import "errors"

var (
	ErrInvalidPartitionCount = errors.New("invalid target partition count")
	ErrRebalanceInProgress   = errors.New("rebalance already in progress")
	ErrNotMigrating          = errors.New("no migration in progress")
	ErrMigrationIncomplete   = errors.New("migration not complete")
	ErrNewKeyDuringMigration = errors.New("writing brand-new keys during migration is rejected")
)
