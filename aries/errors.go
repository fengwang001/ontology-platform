package aries

import "errors"

var (
	ErrInvalidRecord              = errors.New("invalid log record")
	ErrNonIncreasingLSN           = errors.New("LSN must be greater than the current maximum LSN")
	ErrBeginCheckpointNotFound    = errors.New("EndCkpt begin LSN does not refer to an existing BeginCkpt")
	ErrCheckpointAlreadyCompleted = errors.New("BeginCkpt already has an EndCkpt")
	ErrEndedTransaction           = errors.New("record references a transaction that has ended")
	ErrUnknownTransaction         = errors.New("Commit, Abort, or End references a transaction that never appeared")
)
