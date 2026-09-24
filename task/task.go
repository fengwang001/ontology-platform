// Package task defines the unit of work submitted by a tenant.
package task

import (
	"errors"
	"fmt"
)

// ErrBadCost is returned when a task cost is negative.
var ErrBadCost = errors.New("task: cost must be non-negative")

// ValidateCost checks that cost is a legal non-negative integer cost.
func ValidateCost(cost int64) error {
	if cost < 0 {
		return ErrBadCost
	}
	return nil
}

// Task is a unit of work: the owning tenant, its integer cost in scheduling
// units, and its per-tenant submission sequence number (0-based FIFO order).
type Task struct {
	Tenant string
	Cost   int64
	Seq    int64
}

// Key uniquely identifies a task within a single scheduler instance.
func (t Task) Key() string {
	return fmt.Sprintf("%s#%d", t.Tenant, t.Seq)
}

// Zero reports whether t is the zero value (used to signal "no task").
func (t Task) Zero() bool {
	return t.Tenant == "" && t.Cost == 0 && t.Seq == 0
}
