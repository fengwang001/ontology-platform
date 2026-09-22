package correlate

import "ontology/slot"

// Deliver attempts to match a reply to the in-flight request identified by
// the token. Exactly one delivery per request succeeds: a second reply for
// the same generation is rejected. Late replies for an old generation of a
// reused ID are rejected as ErrOrphanStale and never complete the current
// generation. The three orphan classes are ErrOrphanUnknown, ErrOrphanIdle
// and ErrOrphanStale.
func (c *Correlator) Deliver(tok Token) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(c.now())

	s, ok := c.slotAt(tok.ID)
	if !ok {
		c.c.OrphanUnknown++
		return ErrOrphanUnknown
	}

	switch s.State() {
	case slot.Waiting:
		if !s.Complete(tok.Epoch) {
			// The ID is held, but by a newer generation: a late reply.
			c.c.OrphanStale++
			return ErrOrphanStale
		}
		c.c.Completed++
		_ = c.pool.Release(tok.ID)
		_ = s.Release()
		return nil
	case slot.Vacant:
		if s.Epoch() == 0 {
			c.c.OrphanUnknown++
			return ErrOrphanUnknown
		}
		c.c.OrphanIdle++
		return ErrOrphanIdle
	default:
		// Defensive: finished-but-unreleased states are not produced because
		// every terminal transition releases the slot.
		c.c.OrphanIdle++
		return ErrOrphanIdle
	}
}

// Cancel cancels the in-flight request for the token and frees its ID. It
// returns ErrNotInFlight for IDs that are unknown, already finished, or whose
// token belongs to an older generation; in every failure case no state or
// counter changes.
func (c *Correlator) Cancel(tok Token) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(c.now())

	s, ok := c.slotAt(tok.ID)
	if !ok || s.State() != slot.Waiting || s.Epoch() != tok.Epoch {
		return ErrNotInFlight
	}
	if !s.Cancel(tok.Epoch) {
		return ErrNotInFlight
	}
	c.c.Canceled++
	_ = c.pool.Release(tok.ID)
	_ = s.Release()
	return nil
}

// slotAt returns the slot for an in-range id.
func (c *Correlator) slotAt(id int) (*slot.Slot, bool) {
	if id < 0 || id >= len(c.slots) {
		return nil, false
	}
	return c.slots[id], true
}

// Lookup reports the current state of an ID after applying lazy timeouts.
// Unknown and finished IDs yield the zero Status with ok == false. Two calls
// at the same injected time return identical results.
func (c *Correlator) Lookup(id int) (Status, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.sweep(now)

	s, ok := c.slotAt(id)
	if !ok || s.State() != slot.Waiting {
		return Status{}, false
	}
	remaining := s.Deadline().Sub(now)
	return Status{Waiting: true, Epoch: s.Epoch(), Remaining: remaining}, true
}
