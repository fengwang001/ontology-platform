// Package arr validates inputs and exposes sentinel errors before
// delegating the computation to package kad.
package arr

import (
	"errors"

	"ontology/kad"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrNil      = errors.New("arr: nil slice")
	ErrEmpty    = errors.New("arr: empty slice")
	ErrTooLarge = errors.New("arr: slice too large")
)

// MaxLen is the largest accepted input length.
const MaxLen = 1 << 30

// Validate reports whether a is an acceptable non-empty input.
func Validate(a []int) error {
	switch {
	case a == nil:
		return ErrNil
	case len(a) == 0:
		return ErrEmpty
	case len(a) > MaxLen:
		return ErrTooLarge
	}
	return nil
}

// MaxSum validates a and returns its maximum non-empty subarray sum.
func MaxSum(a []int) (int, error) {
	if err := Validate(a); err != nil {
		return 0, err
	}
	return kad.MaxSubarraySum(a), nil
}

// MaxRange validates a and returns the half-open range and sum of a
// maximum non-empty subarray.
func MaxRange(a []int) (lo, hi, sum int, err error) {
	if err := Validate(a); err != nil {
		return 0, 0, 0, err
	}
	lo, hi, sum = kad.MaxSubarrayRange(a)
	return lo, hi, sum, nil
}
