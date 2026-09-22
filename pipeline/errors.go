package pipeline

import "errors"

var (
	// ErrClosed is returned by Ingest after Close completed the ingest
	// phase (or after a fault-injected crash). It is definitive: callers
	// must not retry the record.
	ErrClosed = errors.New("pipeline: closed")
	// ErrRecordTooLarge wraps budget.ErrRecordTooLarge: a record whose
	// own size exceeds the whole budget can never be spilled around.
	ErrRecordTooLarge = errors.New("pipeline: record larger than memory budget")
	// ErrStateCorrupt means the checkpoint itself is unusable.
	ErrStateCorrupt = errors.New("pipeline: checkpoint corrupt")
)
