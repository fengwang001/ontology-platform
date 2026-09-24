// Package wrap implements uint32 -> int64 offset unwrap arithmetic and
// classification. It depends on no other package in this module.
package wrap

import (
	"errors"
	"math"
)

// Event is the classification of an incoming raw offset.
type Event uint8

const (
	// First is emitted for the very first accepted offset.
	First Event = iota
	// Forward means r > prev (normal advance without wrap).
	Forward
	// Wrap means r < prev and the drop exceeds the threshold: the 32-bit
	// counter rolled over to a small value.
	Wrap
	// Duplicate means r == prev.
	Duplicate
)

// Size is the modulus of the raw 32-bit offset space.
const Size uint64 = 1 << 32

// Sentinel errors (re-exported by eng and api).
var (
	// ErrRegression is a backwards move with drop <= threshold: rejected.
	ErrRegression = errors.New("wrap: offset regression")
	// ErrOverflow means the unwrapped value would exceed math.MaxInt64.
	ErrOverflow = errors.New("wrap: unwrapped offset overflow")
)

// Classify decides the event for raw offset r against the last accepted raw
// offset prev. hasPrev is false only for the first offset of a stream.
// Threshold validation (1 <= threshold < 2^31) is the caller's job.
func Classify(prev, r uint32, hasPrev bool, threshold uint32) (Event, error) {
	if !hasPrev {
		return First, nil
	}
	switch {
	case r == prev:
		return Duplicate, nil
	case r > prev:
		return Forward, nil
	default: // r < prev
		if uint64(prev)-uint64(r) > uint64(threshold) {
			return Wrap, nil
		}
		return 0, ErrRegression
	}
}

// Unwrap computes the new 64-bit unwrapped value from the previous unwrapped
// value pu, the raw pair (prev, r) and the event returned by Classify.
// It returns ErrOverflow without a result if the value would overflow int64.
func Unwrap(pu int64, prev, r uint32, ev Event) (int64, error) {
	switch ev {
	case First:
		return int64(r), nil // r < 2^32, cannot overflow
	case Duplicate:
		return pu, nil
	case Forward:
		return addChecked(pu, uint64(r)-uint64(prev))
	case Wrap:
		// Distance travelled across the modulus: (2^32 - prev) + r.
		return addChecked(pu, Size-uint64(prev)+uint64(r))
	default:
		return 0, ErrOverflow
	}
}

// addChecked adds a non-negative delta to pu and refuses to exceed MaxInt64.
func addChecked(pu int64, delta uint64) (int64, error) {
	v := uint64(pu) + delta // accepted pu is always non-negative
	if v > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return int64(v), nil
}
