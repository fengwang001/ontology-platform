// Package slot accounts for the in-flight slots of an OTA campaign.
//
// A Counter bounds how many devices may be InFlight at any moment.
// It is a pure accounting device: the campaign layer decides when to
// acquire and release, the counter only enforces the capacity invariant.
package slot

import "errors"

// ErrInvalid reports an out-of-range capacity.
var ErrInvalid = errors.New("slot: invalid capacity")

const (
	minCapacity = 1
	maxCapacity = 10_000
)

// Counter tracks used in-flight slots against a fixed capacity.
type Counter struct {
	capacity int
	used     int
}

// New returns a Counter with the given capacity, which must be in
// [1, 10^4].
func New(capacity int) (*Counter, error) {
	if capacity < minCapacity || capacity > maxCapacity {
		return nil, ErrInvalid
	}
	return &Counter{capacity: capacity}, nil
}

// Free returns the number of slots still available.
func (c *Counter) Free() int { return c.capacity - c.used }

// Used returns the number of slots currently held.
func (c *Counter) Used() int { return c.used }

// Acquire takes one slot, reporting whether one was available.
func (c *Counter) Acquire() bool {
	if c.used >= c.capacity {
		return false
	}
	c.used++
	return true
}

// Release returns one slot. Releasing more than was acquired is a
// no-op, keeping the used count sane for rollback replay.
func (c *Counter) Release() {
	if c.used > 0 {
		c.used--
	}
}
