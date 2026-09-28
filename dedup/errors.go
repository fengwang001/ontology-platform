package dedup

import "errors"

// Distinguishable rejection reasons. Use errors.Is to match.
var (
	// ErrNegativePartition: record partition is negative.
	ErrNegativePartition = errors.New("dedup: negative partition")
	// ErrPartitionLimit: record partition exceeds the configured partition count.
	ErrPartitionLimit = errors.New("dedup: partition exceeds limit")
	// ErrNegativeOffset: record offset is negative.
	ErrNegativeOffset = errors.New("dedup: negative offset")
	// ErrEmptyKey: record key is empty.
	ErrEmptyKey = errors.New("dedup: empty key")
	// ErrOutOfOrder: offsets of one partition are not strictly increasing within a batch.
	ErrOutOfOrder = errors.New("dedup: offsets not strictly increasing within batch")
	// ErrInvalidPartitionCount: Open was called with a non-positive partition count.
	ErrInvalidPartitionCount = errors.New("dedup: partition count must be positive")
)
