// Package policy parses and normalizes rate-limit quotas.
package policy

import (
	"errors"
	"math"
)

// Quota describes a token bucket's limits.
//
// Capacity is the bucket size and therefore the burst limit: a single
// request larger than Capacity can never be served. RatePerSec is the
// continuous refill rate in tokens per second.
type Quota struct {
	Capacity   float64
	RatePerSec float64
}

// Validation errors, distinguishable with errors.Is.
var (
	// ErrCapacity is returned when capacity is not a finite number > 0.
	ErrCapacity = errors.New("policy: capacity must be a finite number > 0")
	// ErrRate is returned when the refill rate is not a finite number >= 0.
	ErrRate = errors.New("policy: rate must be a finite number >= 0")
)

// Normalize validates capacity and ratePerSec and returns the
// corresponding Quota. capacity must be finite and > 0 (it is the burst
// limit); ratePerSec must be finite and >= 0 (0 means never refill).
func Normalize(capacity, ratePerSec float64) (Quota, error) {
	if math.IsNaN(capacity) || math.IsInf(capacity, 0) || capacity <= 0 {
		return Quota{}, ErrCapacity
	}
	if math.IsNaN(ratePerSec) || math.IsInf(ratePerSec, 0) || ratePerSec < 0 {
		return Quota{}, ErrRate
	}
	return Quota{Capacity: capacity, RatePerSec: ratePerSec}, nil
}

// Must is a helper for static configuration: it panics on invalid input.
func Must(capacity, ratePerSec float64) Quota {
	q, err := Normalize(capacity, ratePerSec)
	if err != nil {
		panic(err)
	}
	return q
}
