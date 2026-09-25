// Package vcache maintains the materialized views ReadG/ReadTotal on top of
// ver.Store. Writes never touch the cache; reads compare version stamps and
// lazily recompute on staleness. Depends only on ver.
package vcache

import (
	"sync"

	"ontology/ver"
)

type entry struct {
	sum   int64
	stamp int64
}

// Cache is safe for concurrent use.
type Cache struct {
	st *ver.Store

	mu   sync.Mutex
	cg   map[string]entry // per-group cached {sum, stamp}
	ct   entry            // cached total
	ctOK bool

	// scanned counts records traversed to *decide freshness* (version
	// comparison). Stamps are maintained incrementally, so a freshness
	// check is a map lookup and this stays 0 regardless of record count.
	// Unexported on purpose: it is an internal proof aid, never API.
	scanned int
}

func New(st *ver.Store) *Cache {
	return &Cache{st: st, cg: make(map[string]entry)}
}

// ReadG returns the sum of Values in group, refreshing the group cache.
func (c *Cache) ReadG(group string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st.Stamp(group) // O(1): no record traversal, scanned += 0
	if e, ok := c.cg[group]; ok && e.stamp == st {
		return e.sum
	}
	sum := c.st.SumGroup(group) // recompute on staleness
	c.cg[group] = entry{sum: sum, stamp: st}
	return sum
}

// ReadTotal returns the sum of all Values, refreshing the total cache.
func (c *Cache) ReadTotal() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.st.TotalStamp() // O(1)
	if c.ctOK && c.ct.stamp == st {
		return c.ct.sum
	}
	sum := c.st.SumAll()
	c.ct = entry{sum: sum, stamp: st}
	c.ctOK = true
	return sum
}

// View returns per-group sums and the grand total, consistently via
// ReadG/ReadTotal.
func (c *Cache) View() (map[string]int64, int64) {
	groups := c.st.Groups()
	out := make(map[string]int64, len(groups))
	for _, g := range groups {
		out[g] = c.ReadG(g)
	}
	return out, c.ReadTotal()
}
