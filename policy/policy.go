// Package policy parses and normalizes rate-limit quotas: capacity
// (burst limit) and refill rate per second. It validates inputs and
// clamps them to safe bounds. It has no dependencies on other
// packages.
package policy

import (
	"errors"
	"math"
)

// Quota describes a token bucket's static configuration.
type Quota struct {
	// Capacity is the bucket capacity and also the burst limit: a
	// single request larger than Capacity can never be admitted.
	Capacity float64
	// RatePerSec is the continuous refill rate in tokens per second.
	RatePerSec float64
}

// Bounds for normalized quotas.
const (
	// MinCapacity is the smallest legal capacity.
	MinCapacity = 1.0
	// MaxCapacity guards against absurd or non-finite capacities.
	MaxCapacity = 1e12
	// MaxRatePerSec guards against absurd or non-finite rates.
	MaxRatePerSec = 1e12
)

// ErrNonFinite is returned when capacity or rate is NaN or infinite.
var ErrNonFinite = errors.New("policy: capacity and rate must be finite")

// ErrCapacityRange is returned when capacity falls outside
// [MinCapacity, MaxCapacity].
var ErrCapacityRange = errors.New("policy: capacity out of range")

// ErrNegativeRate is returned when the refill rate is negative.
var ErrNegativeRate = errors.New("policy: rate must not be negative")

// Normalize validates q and returns the canonical quota to use.
//
// Rules:
//   - NaN or infinite values are rejected with ErrNonFinite.
//   - Capacity must lie within [MinCapacity, MaxCapacity]; fractional
//     capacities are rounded up so the burst limit is an integer.
//   - A negative rate is rejected; a positive rate is clamped to
//     MaxRatePerSec; zero means "never refills" and is legal.
func Normalize(q Quota) (Quota, error) {
	if math.IsNaN(q.Capacity) || math.IsInf(q.Capacity, 0) ||
		math.IsNaN(q.RatePerSec) || math.IsInf(q.RatePerSec, 0) {
		return Quota{}, ErrNonFinite
	}
	if q.Capacity < MinCapacity || q.Capacity > MaxCapacity {
		return Quota{}, ErrCapacityRange
	}
	if q.RatePerSec < 0 {
		return Quota{}, ErrNegativeRate
	}
	capacity := math.Ceil(q.Capacity)
	rate := q.RatePerSec
	if rate > MaxRatePerSec {
		rate = MaxRatePerSec
	}
	return Quota{Capacity: capacity, RatePerSec: rate}, nil
}

// MustNormalize is like Normalize but panics on invalid input. It is
// intended for static configuration known to be valid.
func MustNormalize(q Quota) Quota {
	n, err := Normalize(q)
	if err != nil {
		panic(err)
	}
	return n
}
