// Package api is the public face of the SpaceSaving top-K counter.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/ss"
)

// Key is an event key; must be >= 0.
type Key = int

type Entry = ss.Entry // one counter: (Key, Count, Err)

// Decidable sentinel errors, all mutually distinct.
var (
	ErrBadK        = errors.New("api: k must be >= 1")
	ErrNegativeKey = errors.New("api: negative key")
	ErrNilFeed     = errors.New("api: nil feed")
)

// Counter is a concurrency-safe SpaceSaving summary.
type Counter struct {
	mu sync.RWMutex
	s  *ss.Summary
}

// New returns a counter with k slots; k < 1 fails with ErrBadK.
func New(k int) (*Counter, error) {
	if k < 1 {
		return nil, ErrBadK
	}
	return &Counter{s: ss.New(k)}, nil
}

// Feed validates the whole batch first: a rejected batch (nil slice,
// negative key) leaves the state untouched.
func (c *Counter) Feed(keys []Key) error {
	if keys == nil {
		return ErrNilFeed
	}
	for _, k := range keys {
		if k < 0 {
			return ErrNegativeKey
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		c.s.Add(k)
	}
	return nil
}

// Query returns the overestimate for x (0 if unmonitored).
func (c *Counter) Query(x Key) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.s.Query(x)
}

// TopK returns all counters sorted by Count desc, then Key asc.
func (c *Counter) TopK() []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.s.TopK()
}

// SelfCheck verifies the four invariants on built-in event streams.
// It only touches fresh local instances, so it is concurrency-safe.
func (c *Counter) SelfCheck() error {
	// Canonical replay of k=3, stream [3,1,3,2,4,1,3,5] (invariant 3).
	c3, _ := New(3)
	_ = c3.Feed([]Key{3, 1, 3, 2, 4, 1, 3, 5})
	want := []Entry{{Key: 3, Count: 3}, {Key: 5, Count: 3, Err: 2}, {Key: 4, Count: 2, Err: 1}}
	if got := c3.TopK(); fmt.Sprint(got) != fmt.Sprint(want) {
		return fmt.Errorf("api: canonical replay = %v, want %v", got, want)
	}
	if err := checkStreams(); err != nil {
		return err
	}
	return checkRejects()
}

// checkStreams compares against naive exact counts on generated
// streams (invariants 1, 2 and capacity).
func checkStreams() error {
	for _, k := range []int{1, 3, 10} {
		c, _ := New(k)
		tru := map[int]int{}
		seed := uint64(k*2654435761 + 1)
		for i := 0; i < 300; i++ {
			seed = seed*6364136223846793005 + 1442695040888963407
			x := int(seed>>33) % (3*k + 1)
			tru[x]++
			_ = c.Feed([]Key{x})
			if len(c.TopK()) > k {
				return fmt.Errorf("api: capacity %d exceeded", k)
			}
		}
		entries := c.TopK()
		min := entries[len(entries)-1].Count // TopK is count-desc
		mon := map[int]Entry{}
		for _, e := range entries {
			mon[e.Key] = e
		}
		for x, tc := range tru {
			if e, ok := mon[x]; ok {
				if q := c.Query(x); q < tc || e.Count-e.Err > tc {
					return fmt.Errorf("api: bound key %d: %d vs [%d,%d]", x, tc, e.Count-e.Err, q)
				}
			} else if tc > min {
				return fmt.Errorf("api: min bound key %d: %d > %d", x, tc, min)
			}
		}
	}
	return nil
}

// checkRejects verifies decidable, distinct errors that leave no
// trace (invariant 4).
func checkRejects() error {
	if _, err := New(0); !errors.Is(err, ErrBadK) {
		return fmt.Errorf("api: New(0) err = %v", err)
	}
	c, _ := New(3)
	_ = c.Feed([]Key{1, 2, 3})
	before := fmt.Sprint(c.TopK())
	if err := c.Feed(nil); !errors.Is(err, ErrNilFeed) {
		return fmt.Errorf("api: Feed(nil) err = %v", err)
	}
	if err := c.Feed([]Key{1, -1}); !errors.Is(err, ErrNegativeKey) {
		return fmt.Errorf("api: Feed(neg) err = %v", err)
	}
	if fmt.Sprint(c.TopK()) != before {
		return errors.New("api: rejected feed changed state")
	}
	if errors.Is(ErrBadK, ErrNegativeKey) || errors.Is(ErrBadK, ErrNilFeed) || errors.Is(ErrNegativeKey, ErrNilFeed) {
		return errors.New("api: sentinel errors not distinct")
	}
	return nil
}
