// Package pmtu implements a path MTU discovery cache keyed by destination.
//
// The cache records, per destination, the currently assumed path MTU, the
// moment it was set, and a consecutive-timeout counter. Values only decrease
// while an entry is valid; once an entry expires the destination falls back
// to the egress interface MTU.
package pmtu

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrInvalidLo        = errors.New("pmtu: lower bound Lo must be positive")
	ErrInvalidE         = errors.New("pmtu: interface MTU E must be >= Lo")
	ErrInvalidPlateaus  = errors.New("pmtu: plateaus P must be strictly descending with every item in [Lo, E]")
	ErrInvalidExpiry    = errors.New("pmtu: validity period X must be positive")
	ErrInvalidThreshold = errors.New("pmtu: blackhole threshold K must be positive")
	ErrClockRollback    = errors.New("pmtu: clock rollback: timestamp predates the entry's set time")
	ErrEmptyDestination = errors.New("pmtu: destination must not be empty")
	ErrInvalidSize      = errors.New("pmtu: packet size must be in (0, E]")
	ErrInvalidMTU       = errors.New("pmtu: reported MTU must be in [0, E]")
)

// entry is the per-destination record: path MTU, the moment it was set, and
// the consecutive-timeout counter.
type entry struct {
	pmtu     int
	setAt    time.Time
	timeouts int
}

// Cache is a concurrency-safe path MTU discovery cache.
type Cache struct {
	mu        sync.Mutex
	e         int
	lo        int
	plateaus  []int // strictly descending, each in [lo, e]
	expiry    time.Duration
	threshold int
	entries   map[string]entry
}

// NewCache builds a cache. E is the egress interface MTU, Lo the lower bound,
// P the strictly descending plateau table (each item within [Lo, E]), X the
// entry validity period, and K the blackhole timeout threshold. Parameters
// are rejected in the order: Lo, E, P, X, K.
func NewCache(E, Lo int, P []int, X time.Duration, K int) (*Cache, error) {
	if Lo <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidLo, Lo)
	}
	if E < Lo {
		return nil, fmt.Errorf("%w: E=%d Lo=%d", ErrInvalidE, E, Lo)
	}
	for i, p := range P {
		if p < Lo || p > E {
			return nil, fmt.Errorf("%w: P[%d]=%d out of [%d, %d]", ErrInvalidPlateaus, i, p, Lo, E)
		}
		if i > 0 && P[i-1] <= p {
			return nil, fmt.Errorf("%w: P[%d]=%d not less than P[%d]=%d", ErrInvalidPlateaus, i, p, i-1, P[i-1])
		}
	}
	if X <= 0 {
		return nil, fmt.Errorf("%w: got %s", ErrInvalidExpiry, X)
	}
	if K <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidThreshold, K)
	}
	plateaus := make([]int, len(P))
	copy(plateaus, P)
	return &Cache{
		e:         E,
		lo:        Lo,
		plateaus:  plateaus,
		expiry:    X,
		threshold: K,
		entries:   make(map[string]entry),
	}, nil
}

// currentLocked returns the live entry for dest, treating an expired entry as
// absent (and removing it). An entry is expired once now >= setAt + X.
func (c *Cache) currentLocked(dest string, now time.Time) (entry, bool) {
	ent, ok := c.entries[dest]
	if !ok {
		return entry{}, false
	}
	if !now.Before(ent.setAt.Add(c.expiry)) {
		delete(c.entries, dest)
		return entry{}, false
	}
	return ent, true
}

// nextBelow returns the largest plateau strictly below v, or Lo if none.
func (c *Cache) nextBelow(v int) int {
	for _, p := range c.plateaus {
		if p < v {
			return p
		}
	}
	return c.lo
}

// Query returns the cached path MTU for dest, or E when no valid entry exists.
func (c *Cache) Query(dest string, now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.currentLocked(dest, now); ok {
		return ent.pmtu
	}
	return c.e
}

// validateReportLocked applies the common report checks in the specified
// order: clock rollback, empty destination, invalid size. It also returns the
// live entry, if any.
func (c *Cache) validateReportLocked(dest string, size int, now time.Time) (entry, bool, error) {
	ent, ok := c.currentLocked(dest, now)
	if ok && now.Before(ent.setAt) {
		return entry{}, false, fmt.Errorf("%w: now=%s setAt=%s", ErrClockRollback, now, ent.setAt)
	}
	if dest == "" {
		return entry{}, false, ErrEmptyDestination
	}
	if size <= 0 || size > c.e {
		return entry{}, false, fmt.Errorf("%w: size=%d E=%d", ErrInvalidSize, size, c.e)
	}
	return ent, ok, nil
}

// ReportFragmentationNeeded handles a "fragmentation needed" report for a
// rejected packet of length size, where m is the MTU reported by the router
// (0 means unreported). The new value only applies when it is strictly lower
// than the current path MTU; ignored reports do not refresh the set time.
func (c *Cache) ReportFragmentationNeeded(dest string, size, m int, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ent, ok, err := c.validateReportLocked(dest, size, now)
	if err != nil {
		return err
	}
	if m < 0 || m > c.e {
		return fmt.Errorf("%w: m=%d E=%d", ErrInvalidMTU, m, c.e)
	}
	current := c.e
	if ok {
		current = ent.pmtu
	}
	if m == 0 || m >= size {
		// Unreported or unusable: fall back to the plateau table.
		m = c.nextBelow(size)
	}
	if m < c.lo {
		m = c.lo
	}
	if m >= current {
		// Stale or non-lowering report: ignore without refreshing setAt.
		return nil
	}
	ent.pmtu = m
	ent.setAt = now
	c.entries[dest] = ent
	return nil
}

// ReportTimeout records a timeout for a packet of length size. Only packets
// whose length equals the current path MTU count; when the consecutive count
// reaches K the path MTU drops to the next plateau (or Lo), the count resets,
// and the set time moves to now.
func (c *Cache) ReportTimeout(dest string, size int, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ent, ok, err := c.validateReportLocked(dest, size, now)
	if err != nil {
		return err
	}
	current := c.e
	if ok {
		current = ent.pmtu
	}
	if size != current {
		return nil
	}
	if !ok {
		ent = entry{pmtu: c.e, setAt: now}
	}
	ent.timeouts++
	if ent.timeouts >= c.threshold {
		ent.pmtu = c.nextBelow(ent.pmtu)
		ent.timeouts = 0
		ent.setAt = now
	}
	c.entries[dest] = ent
	return nil
}

// ReportSuccess records a successful delivery of a packet of length size and
// clears the consecutive-timeout counter when size equals the current path
// MTU. It never creates an entry.
func (c *Cache) ReportSuccess(dest string, size int, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ent, ok, err := c.validateReportLocked(dest, size, now)
	if err != nil {
		return err
	}
	if !ok || size != ent.pmtu {
		return nil
	}
	ent.timeouts = 0
	c.entries[dest] = ent
	return nil
}
