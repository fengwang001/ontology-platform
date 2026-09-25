// Package arr validates input slices before delegating to package kad.
package arr

import (
	"errors"

	"ontology/kad"
)

// MaxLen is the largest accepted input length (sanity cap).
const MaxLen = 1 << 20

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrNil      = errors.New("arr: nil slice")
	ErrEmpty    = errors.New("arr: empty slice")
	ErrTooLarge = errors.New("arr: slice exceeds MaxLen")
)

// Validate reports whether arr is usable as algorithm input.
func Validate(arr []int) error {
	switch {
	case arr == nil:
		return ErrNil
	case len(arr) == 0:
		return ErrEmpty
	case len(arr) > MaxLen:
		return ErrTooLarge
	}
	return nil
}

// MaxSum validates arr, then returns its maximum non-empty subarray sum.
func MaxSum(arr []int) (int, error) {
	if err := Validate(arr); err != nil {
		return 0, err
	}
	return kad.MaxSubarraySum(arr), nil
}

// MaxRange validates arr, then returns a maximum-sum subarray range and sum.
func MaxRange(arr []int) (lo, hi, sum int, err error) {
	if err := Validate(arr); err != nil {
		return 0, -1, 0, err
	}
	lo, hi, sum = kad.MaxSubarrayRange(arr)
	return lo, hi, sum, nil
}
