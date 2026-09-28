package idempotency

import "errors"

var (
	ErrInvalidRequest         = errors.New("idempotency: invalid request")
	ErrProducerFenced         = errors.New("idempotency: producer fenced by newer epoch")
	ErrOutOfOrderSequence     = errors.New("idempotency: out of order sequence")
	ErrDuplicateSequenceStale = errors.New("idempotency: duplicate sequence evicted from window")
)
