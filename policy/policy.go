// Package policy validates and normalizes rate-limit quotas. It has no
// dependencies on other packages in this module.
package policy

import (
	"errors"
	"fmt"
	"math"
)

var (
	// ErrInvalidCapacity reports a non-positive or non-finite capacity.
	ErrInvalidCapacity = errors.New("policy: capacity must be a positive finite number")
	// ErrInvalidRate reports a non-positive or non-finite refill rate.
	ErrInvalidRate = errors.New("policy: refill rate must be a positive finite number")
)

// Quota describes one token bucket. Capacity is also the burst limit: a
// single request larger than Capacity can never be admitted. RatePerSec
// is the continuous refill rate in tokens per second.
type Quota struct {
	Capacity   float64
	RatePerSec float64
}

// Validate checks capacity and rate and returns the normalized Quota.
func Validate(capacity, ratePerSec float64) (Quota, error) {
	q := Quota{Capacity: capacity, RatePerSec: ratePerSec}
	if err := q.Check(); err != nil {
		return Quota{}, err
	}
	return q, nil
}

// Check reports whether the quota is legal.
func (q Quota) Check() error {
	if !positiveFinite(q.Capacity) {
		return fmt.Errorf("%w: got %v", ErrInvalidCapacity, q.Capacity)
	}
	if !positiveFinite(q.RatePerSec) {
		return fmt.Errorf("%w: got %v", ErrInvalidRate, q.RatePerSec)
	}
	return nil
}

func positiveFinite(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}
