package correlate

import (
	"time"

	"ontology/slot"
)

// Info describes one ID as seen by a read-only query.
type Info struct {
	State     slot.State
	Remaining time.Duration
	InFlight  bool
}

// Lookup reports the current state of id. Unknown or settled IDs are zero.
//
// Lookup does not run lazy timeout expiration, so two consecutive Lookup
// calls at the same injected time always return identical results. Pending
// requests are therefore reported even if their deadline has already passed;
// their lateness becomes visible on the next mutating operation (Request,
// Deliver or Cancel), which performs lazy timeout cleanup.
func (c *Correlator) Lookup(id slot.ID) Info {
	c.mu.Lock()
	defer c.mu.Unlock()

	index := id.Index()
	if int(index) >= len(c.ever) || !c.ever[index] {
		return Info{}
	}
	s := c.slots[index]
	if s.Generation() != id.Generation() || s.State() != slot.Pending {
		return Info{}
	}
	return Info{State: slot.Pending, Remaining: s.Remaining(c.now()), InFlight: true}
}

// InFlight reports the current number of pending requests.
func (c *Correlator) InFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alloc.InUse()
}

// Counters returns a copy of the delivery counters.
func (c *Correlator) Counters() Counters {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counters
}
