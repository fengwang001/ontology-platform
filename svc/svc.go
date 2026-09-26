// Package svc is the server registry: index-to-weight mapping, validation,
// and a validating wrapper around the wrr core.
package svc

import (
	"errors"

	"ontology/wrr"
)

// Sentinel errors, mutually distinct, returned before any state is touched.
var (
	ErrInvalidConfig   = errors.New("svc: empty weights or weight <= 0")
	ErrIndexOutOfRange = errors.New("svc: server index out of range")
	ErrInvalidWeight   = errors.New("svc: weight must be >= 1")
)

// Registry maps server indices to weights and validates every mutation.
type Registry struct {
	core *wrr.WRR
}

// New validates the config; on failure nothing is created.
func New(weights []int) (*Registry, error) {
	if len(weights) == 0 {
		return nil, ErrInvalidConfig
	}
	for _, w := range weights {
		if w <= 0 {
			return nil, ErrInvalidConfig
		}
	}
	return &Registry{core: wrr.New(weights)}, nil
}

// Next picks the next server by smooth weighted round-robin.
func (r *Registry) Next() int { return r.core.Next() }

// SetWeight validates i and w before touching any state; rejected calls
// leave weights and current weights untouched.
func (r *Registry) SetWeight(i, w int) error {
	if i < 0 || i >= r.core.Len() {
		return ErrIndexOutOfRange
	}
	if w <= 0 {
		return ErrInvalidWeight
	}
	r.core.SetWeight(i, w)
	return nil
}

// Weight returns w[i], or 0 when i is out of range.
func (r *Registry) Weight(i int) int { return r.core.Weight(i) }

// Len returns the server count.
func (r *Registry) Len() int { return r.core.Len() }

// SumCW returns the sum of all current weights (invariant: always 0).
func (r *Registry) SumCW() int { return r.core.SumCW() }

// CheckScalability reports whether max-location stays sublinear in m.
func CheckScalability() error { return wrr.CheckScalability() }
