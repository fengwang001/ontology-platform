// Package task defines the unit of scheduling: a tenant job with a cost.
package task

import (
	"errors"
	"math"
)

// Boundaries enforced everywhere costs and weights are accepted.
const (
	MaxCost = 1e9
	MinW    = 1e-9
	MaxW    = 1e12
)

var (
	// ErrBadCost is returned when a task cost is negative or out of range.
	ErrBadCost = errors.New("task: bad cost")
	// ErrBadWeight is returned when a weight is zero, negative or out of range.
	ErrBadWeight = errors.New("task: bad weight")
)

// Task is a submitted unit. Seq is the global admission serial number.
type Task struct {
	Tenant string
	Cost   float64
	Seq    int64
}

// New validates and builds a task. Zero cost is legal.
func New(tenant string, cost float64, seq int64) (Task, error) {
	if tenant == "" || math.IsNaN(cost) || math.IsInf(cost, 0) ||
		!(cost >= 0 && cost <= MaxCost) {
		return Task{}, ErrBadCost
	}
	return Task{Tenant: tenant, Cost: cost, Seq: seq}, nil
}

// CheckWeight validates the scheduler weight range.
func CheckWeight(w float64) error {
	if math.IsNaN(w) || math.IsInf(w, 0) || !(w >= MinW && w <= MaxW) {
		return ErrBadWeight
	}
	return nil
}
