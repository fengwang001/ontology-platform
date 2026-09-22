package correlate

import (
	"sync"
	"time"

	"ontology/reclaim"
	"ontology/slot"
)

// Counters aggregates delivery outcomes since construction.
type Counters struct {
	Delivered uint64
	Unknown   uint64
	Idle      uint64
	Stale     uint64
}

// Correlator associates out-of-order responses with in-flight requests.
type Correlator struct {
	mu       sync.Mutex
	now      func() time.Time
	slots    []*slot.Slot
	alloc    *reclaim.Allocator
	ever     []bool
	counters Counters
}

// New builds a Correlator with the given hard capacity and injected clock.
func New(capacity int, now func() time.Time) *Correlator {
	if capacity < 0 {
		capacity = 0
	}
	if now == nil {
		now = func() time.Time { return time.Time{} }
	}
	return &Correlator{
		now:   now,
		slots: make([]*slot.Slot, 0, capacity),
		alloc: reclaim.New(capacity),
		ever:  make([]bool, 0, capacity),
	}
}

// Request opens a new in-flight request and returns its correlation ID.
func (c *Correlator) Request(timeout time.Duration) (id slot.ID, err error) {
	if timeout <= 0 {
		return 0, ErrBadBudget
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.expireDue()
	index, ok := c.alloc.Acquire()
	if !ok {
		return 0, ErrCapacity
	}
	for int(index) >= len(c.slots) {
		c.slots = append(c.slots, slot.New(uint32(len(c.slots))))
		c.ever = append(c.ever, false)
	}
	c.ever[index] = true
	id = c.slots[index].Begin(c.now().Add(timeout))
	return id, nil
}

// Deliver attempts to match a response to its in-flight request.
func (c *Correlator) Deliver(id slot.ID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.expireDue()
	index := id.Index()
	if int(index) >= len(c.ever) || !c.ever[index] {
		c.counters.Unknown++
		return ErrUnknownID
	}
	s := c.slots[index]
	if s.Generation() != id.Generation() {
		c.counters.Stale++
		return ErrStaleID
	}
	if s.State() != slot.Pending {
		c.counters.Idle++
		return ErrIdleID
	}
	if err := s.Complete(); err != nil {
		c.counters.Idle++
		return ErrIdleID
	}
	c.alloc.Release(index)
	c.counters.Delivered++
	return nil
}

// Cancel cancels the pending request identified by id.
func (c *Correlator) Cancel(id slot.ID) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.expireDue()
	index := id.Index()
	if int(index) >= len(c.ever) || !c.ever[index] {
		return ErrUnknownID
	}
	s := c.slots[index]
	if s.Generation() != id.Generation() {
		return ErrStaleID
	}
	if s.State() != slot.Pending {
		return ErrNotInFlight
	}
	if err := s.Cancel(); err != nil {
		return ErrNotInFlight
	}
	c.alloc.Release(index)
	return nil
}

// expireDue lazily times out every pending slot due at the injected time.
func (c *Correlator) expireDue() {
	now := c.now()
	for _, s := range c.slots {
		if s.ExpiredAt(now) {
			_ = s.TimeOut()
			c.alloc.Release(s.Index())
		}
	}
}
