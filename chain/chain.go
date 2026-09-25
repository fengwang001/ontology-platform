// Package chain maintains one key's immutable, ts-ascending version chain.
// It depends only on ver.
package chain

import (
	"sort"
	"sync"

	"ontology/ver"
)

// Chain is an ordered list of versions for a single key.
// Versions are never mutated or removed once inserted.
type Chain struct {
	mu      sync.Mutex
	vers    []ver.Version // ascending by ts, unique ts
	lastCmp int           // versions compared in the most recent AsOf
}

// New returns an empty chain.
func New() *Chain { return &Chain{} }

// Insert places v at its sorted position; ts may arrive out of order.
// The caller guarantees ts is unique within the chain.
func (c *Chain) Insert(v ver.Version) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := sort.Search(len(c.vers), func(i int) bool { return !c.vers[i].Before(v) })
	c.vers = append(c.vers, ver.Version{})
	copy(c.vers[i+1:], c.vers[i:])
	c.vers[i] = v
}

// AsOf returns the newest version with commit ts <= T (inclusive).
// The second result is false when no version has ts <= T.
func (c *Chain) AsOf(T int64) (ver.Version, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cmp := 0
	i := sort.Search(len(c.vers), func(i int) bool {
		cmp++
		return c.vers[i].TS() > T
	})
	c.lastCmp = cmp
	if i == 0 {
		return ver.Version{}, false
	}
	return c.vers[i-1], true
}

// Len returns the number of versions in the chain.
func (c *Chain) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.vers)
}

// Versions returns a copy of the chain in ascending ts order.
func (c *Chain) Versions() []ver.Version {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ver.Version, len(c.vers))
	copy(out, c.vers)
	return out
}
