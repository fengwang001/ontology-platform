// Package task defines the unit of work scheduled by the fair scheduler.
package task

import (
	"errors"
	"time"
)

// Validation errors for task construction.
var (
	ErrEmptyTenant = errors.New("task: empty tenant id")
	ErrBadCost     = errors.New("task: negative or NaN cost")
)

// Clock is the injectable time source used to stamp submission time.
type Clock interface {
	Now() time.Time
}

// SimClock is a manually advanced deterministic clock for tests and demos.
type SimClock struct{ T int64 }

// Now returns the simulated instant as a time.Time.
func (c *SimClock) Now() time.Time { return time.Unix(0, c.T) }

// Advance moves the clock forward by d nanoseconds.
func (c *SimClock) Advance(d time.Duration) { c.T += int64(d) }

// WallClock delegates to time.Now; it is the production clock.
type WallClock struct{}

// Now returns the current wall-clock time.
func (WallClock) Now() time.Time { return time.Now() }

// Task is a tenant's unit of work with an accounting cost.
type Task struct {
	Tenant string
	Cost   float64
	Seq    int64
	At     time.Time
}

// New validates the cost and builds a task. Seq is the global submission ordinal.
func New(tenant string, cost float64, seq int64, at time.Time) (Task, error) {
	if tenant == "" {
		return Task{}, ErrEmptyTenant
	}
	if cost < 0 || cost != cost { // negative or NaN
		return Task{}, ErrBadCost
	}
	return Task{Tenant: tenant, Cost: cost, Seq: seq, At: at}, nil
}
