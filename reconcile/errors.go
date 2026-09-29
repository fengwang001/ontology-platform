// Package reconcile implements range-hash based reconciliation for two
// materialized-view replicas.
package reconcile

import "errors"

// Configuration errors are returned before any key or hash is mutated.
var (
	ErrInvalidFanout  = errors.New("reconcile: fanout must be at least 2")
	ErrInvalidKeyMax  = errors.New("reconcile: keyMax must be at least 1")
	ErrInvalidMaxKeys = errors.New("reconcile: maxKeys must be at least 1 and not greater than keyMax+1")
	ErrKeyOutOfRange  = errors.New("reconcile: key out of range")
	ErrTooManyKeys    = errors.New("reconcile: too many distinct keys")
	ErrShapeMismatch  = errors.New("reconcile: replicas have different key spaces or fanout")
)
