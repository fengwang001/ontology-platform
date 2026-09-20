// Package alloc provides weight-proportional allocation of integer
// monetary amounts (in smallest currency units) with exact conservation.
package alloc

import "errors"

var (
	// ErrEmptyWeights is returned when the weights slice is empty.
	ErrEmptyWeights = errors.New("alloc: weights slice is empty")
	// ErrNegativeWeight is returned when any weight is negative.
	ErrNegativeWeight = errors.New("alloc: weight must not be negative")
	// ErrZeroTotalWeight is returned when all weights sum to zero.
	ErrZeroTotalWeight = errors.New("alloc: total weight is zero")
	// ErrOverflow is returned when an intermediate computation
	// would overflow int64.
	ErrOverflow = errors.New("alloc: integer overflow")
	// ErrInvalidParts is returned by Split when n <= 0.
	ErrInvalidParts = errors.New("alloc: number of parts must be positive")
)
