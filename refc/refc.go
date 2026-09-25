// Package refc implements the per-object reference-counting primitive:
// add, remove, idempotency and zero detection. It depends on nothing.
package refc

// Counter tracks which referrers currently hold one object.
// The reference count is the number of distinct holders.
type Counter struct {
	holders map[string]struct{}
}

// New returns an empty Counter (count 0).
func New() *Counter {
	return &Counter{holders: make(map[string]struct{})}
}

// Acquire makes ref hold the object. It is idempotent: if ref already
// holds it, nothing changes and added is false. Otherwise the count
// increases by one and added is true.
func (c *Counter) Acquire(ref string) (added bool) {
	if _, ok := c.holders[ref]; ok {
		return false
	}
	c.holders[ref] = struct{}{}
	return true
}

// Release removes ref's hold. ok is false if ref did not hold the
// object (nothing changes). zero is true when this release dropped
// the count exactly to 0 — the reclamation moment.
func (c *Counter) Release(ref string) (ok, zero bool) {
	if _, held := c.holders[ref]; !held {
		return false, false
	}
	delete(c.holders, ref)
	return true, len(c.holders) == 0
}

// Count returns the number of distinct current holders.
func (c *Counter) Count() int { return len(c.holders) }

// Holds reports whether ref currently holds the object.
func (c *Counter) Holds(ref string) bool {
	_, ok := c.holders[ref]
	return ok
}

// Zero reports whether the count is 0.
func (c *Counter) Zero() bool { return len(c.holders) == 0 }

// Holders returns a snapshot of the current holder set.
func (c *Counter) Holders() map[string]struct{} {
	out := make(map[string]struct{}, len(c.holders))
	for h := range c.holders {
		out[h] = struct{}{}
	}
	return out
}
