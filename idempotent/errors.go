package idempotent

import "errors"

var (
	// ErrNegativePartition 分区为负。
	ErrNegativePartition = errors.New("idempotent: negative partition")
	// ErrNegativeOffset 位点为负。
	ErrNegativeOffset = errors.New("idempotent: negative offset")
	// ErrEmptyKey 记录键为空。
	ErrEmptyKey = errors.New("idempotent: empty key")
	// ErrOutOfOrder 同批同分区位点未严格递增。
	ErrOutOfOrder = errors.New("idempotent: offsets for a partition must be strictly increasing within a batch")
	// ErrTooManyPartitions 分区数超过上限。
	ErrTooManyPartitions = errors.New("idempotent: partition count exceeds limit")
)
