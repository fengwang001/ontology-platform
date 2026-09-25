// Package refc implements the per-object reference-counting primitive:
// add, remove, idempotency check, and zero detection. It depends on nothing.
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

// Inc records that ref holds the object. It reports whether the
// count actually changed: a ref that already holds is idempotent
// (no double count, no error).
func (c *Counter) Inc(ref string) (changed bool) {
	if _, ok := c.holders[ref]; ok {
		return false
	}
	c.holders[ref] = struct{}{}
	return true
}

// Dec removes ref's hold. It reports whether the hold existed and,
// if so, whether the count has just reached zero (reclaim moment).
func (c *Counter) Dec(ref string) (held bool, zero bool) {
	if _, ok := c.holders[ref]; !ok {
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

// Holders returns a snapshot of current holders (for demo display).
func (c *Counter) Holders() []string {
	out := make([]string, 0, len(c.holders))
	for h := range c.holders {
		out = append(out, h)
	}
	return out
}
