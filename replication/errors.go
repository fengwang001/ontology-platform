package replication

import "errors"

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrUnknownReplica    = errors.New("unknown replica")
	ErrUnknownGeneration = errors.New("unknown generation")
	ErrLogDiverged       = errors.New("log diverged beyond cached generations")
)
