package vvsync

import "errors"

var (
	ErrUnregisteredReplica = errors.New("vvsync: replica is not registered")
	ErrNonContiguous       = errors.New("vvsync: change sequence is not contiguous or out of order")
	ErrInvalidVector       = errors.New("vvsync: version vector is invalid")
	ErrBatchTooLarge       = errors.New("vvsync: change batch exceeds the configured limit")
)
