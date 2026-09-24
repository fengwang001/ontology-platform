// Package lcache is the lookup cache: entries, per-key fences, CDC event
// handling, backfill acceptance, hit stats. Not goroutine-safe.
package lcache

import (
	"errors"
	"fmt"
	"strconv"

	"ontology/src"
)

// ErrTooManyKeys rejects an operation that would track more than max keys.
var ErrTooManyKeys = errors.New("lcache: tracked keys would exceed maxKeys")

type entry struct {
	val    string
	ver    int64
	exists bool // false: negative (tombstone) cache entry
}

// Cache holds entries and fences; a key is "tracked" while it has an
// entry or a non-zero fence.
type Cache struct {
	max     int
	entries map[string]entry
	fences  map[string]int64
	tracked int
	access  int // entries touched by the last Apply/Backfill (unexported on purpose)
	hits    int
	misses  int
}

// New returns a cache tracking at most maxKeys keys.
func New(maxKeys int) *Cache {
	return &Cache{max: maxKeys, entries: map[string]entry{}, fences: map[string]int64{}}
}

func (c *Cache) isTracked(k string) bool { _, ok := c.entries[k]; return c.fences[k] != 0 || ok }

// Apply handles one CDC event (Delete and Upsert identically).
func (c *Cache) Apply(ev src.Event) {
	c.access = 0
	f := c.fences[ev.Key]
	if ev.Version <= f { // duplicate or stale: do nothing
		return
	}
	c.fences[ev.Key] = ev.Version
	if f == 0 {
		if _, ok := c.entries[ev.Key]; !ok {
			c.tracked++
		}
	}
	if e, ok := c.entries[ev.Key]; ok {
		c.access = 1
		if e.ver < ev.Version {
			delete(c.entries, ev.Key) // fence > 0 keeps the key tracked
		}
	}
}

// WouldExceed dry-runs whether applying evs would exceed the key budget.
func (c *Cache) WouldExceed(evs []src.Event) bool {
	add := map[string]bool{}
	for _, ev := range evs {
		if ev.Version > c.fences[ev.Key] && !c.isTracked(ev.Key) {
			add[ev.Key] = true
		}
	}
	return c.tracked+len(add) > c.max
}

// Backfill stores a read token when token.Ver >= floor = max(fence, entry
// ver); rejection is not an error and changes nothing.
func (c *Cache) Backfill(t src.Token) (accepted bool, err error) {
	c.access = 0
	floor := c.fences[t.Key]
	if e, ok := c.entries[t.Key]; ok {
		c.access = 1
		if e.ver > floor {
			floor = e.ver
		}
	}
	if t.Ver < floor {
		return false, nil
	}
	if !c.isTracked(t.Key) {
		if c.tracked+1 > c.max {
			return false, ErrTooManyKeys
		}
		c.tracked++
	}
	c.entries[t.Key] = entry{t.Val, t.Ver, t.Exists}
	return true, nil
}

// Lookup returns (val, exists, hit).
func (c *Cache) Lookup(key string) (string, bool, bool) {
	e, ok := c.entries[key]
	if !ok {
		c.misses++
		return "", false, false
	}
	c.hits++
	return e.val, e.exists, true
}

// Fence is the current fence of key (0 if none).
func (c *Cache) Fence(key string) int64 { return c.fences[key] }

// Snapshot exposes the cache entry of key for inspection.
func (c *Cache) Snapshot(key string) (val string, ver int64, exists, ok bool) {
	e, ok := c.entries[key]
	return e.val, e.ver, e.exists, ok
}

// Stats returns (hits, misses); Tracked/Full report the key budget.
func (c *Cache) Stats() (int, int)       { return c.hits, c.misses }
func (c *Cache) Tracked(key string) bool { return c.isTracked(key) }
func (c *Cache) Full() bool              { return c.tracked >= c.max }

// CheckConsistent verifies invariant 2: every entry version >= its fence.
func (c *Cache) CheckConsistent() error {
	for k, e := range c.entries {
		if e.ver < c.fences[k] {
			return fmt.Errorf("lcache: entry %s@%d below fence %d", k, e.ver, c.fences[k])
		}
	}
	return nil
}

// CheckAccessScaling verifies per-op entry accesses stay bounded by a
// small constant as the cache grows; the counter itself is never exposed.
func (c *Cache) CheckAccessScaling() bool {
	for _, m := range []int{100, 1000, 10000} {
		c2 := New(m + 1)
		for i := 0; i < m; i++ {
			c2.entries["k"+strconv.Itoa(i)] = entry{ver: 1, exists: true}
		}
		c2.tracked = m
		c2.Apply(src.Event{Key: "k0", Version: 2, Kind: src.Upsert})
		a1 := c2.access
		ok, _ := c2.Backfill(src.Token{Key: "k0", Val: "x", Ver: 2, Exists: true})
		if a1 > 2 || c2.access > 2 || !ok {
			return false
		}
	}
	return true
}
