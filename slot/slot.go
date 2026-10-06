// Package slot tracks the in-flight capacity of an OTA campaign.
package slot

import "errors"

// ErrInvalid reports a capacity outside [1, 10000].
var ErrInvalid = errors.New("slot: invalid capacity")

const (
	minCapacity = 1
	maxCapacity = 10_000
)

// Slot is a counter of in-flight slots. It is not goroutine-safe; callers
// serialize access.
type Slot struct {
	capacity int
	used     int
}

// New returns a slot counter with the given capacity.
func New(capacity int) (*Slot, error) {
	if capacity < minCapacity || capacity > maxCapacity {
		return nil, ErrInvalid
	}
	return &Slot{capacity: capacity}, nil
}

// Free returns the number of available slots.
func (s *Slot) Free() int { return s.capacity - s.used }

// Used returns the number of occupied slots.
func (s *Slot) Used() int { return s.used }

// Acquire takes one slot, reporting whether one was available.
func (s *Slot) Acquire() bool {
	if s.used >= s.capacity {
		return false
	}
	s.used++
	return true
}

// Release returns one slot.
func (s *Slot) Release() {
	if s.used > 0 {
		s.used--
	}
}
