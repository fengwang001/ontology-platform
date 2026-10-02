// Package gdsfcache implements a cost-aware build-artifact cache that
// evicts entries by Greedy-Dual-Size-Frequency (GDSF) priority under a
// byte capacity limit. All priorities and the global inflation value are
// exact rational numbers (math/big.Rat); no floating point is used, so
// inflation advancement, priority recomputation on hits, and tie breaking
// are exactly reproducible.
package gdsfcache

import (
	"container/heap"
	"errors"
	"math/big"
	"sync"
)

var (
	// ErrNonPositiveCapacity is returned by New when Cap <= 0.
	ErrNonPositiveCapacity = errors.New("gdsfcache: capacity must be greater than 0")
	// ErrEmptyKey is returned when an operation is given an empty key.
	ErrEmptyKey = errors.New("gdsfcache: key must not be empty")
	// ErrNonPositiveSize is returned by Put when size <= 0.
	ErrNonPositiveSize = errors.New("gdsfcache: size must be a positive number of bytes")
	// ErrInvalidCost is returned by Put when cost < 1.
	ErrInvalidCost = errors.New("gdsfcache: cost must be at least 1")
	// ErrSizeExceedsCapacity is returned by Put when size > Cap.
	ErrSizeExceedsCapacity = errors.New("gdsfcache: size exceeds cache capacity")
	// ErrNotFound is returned by Peek when the key is not cached.
	ErrNotFound = errors.New("gdsfcache: key not found")
)

// Info is a snapshot of a cached entry returned by Peek.
type Info struct {
	Freq int64
	H    *big.Rat
	Last int64
}

// Cache is a concurrency-safe GDSF cache. The zero value is not usable;
// construct one with New.
type Cache struct {
	mu    sync.Mutex
	cap   int64
	used  int64
	l     *big.Rat
	tick  int64
	byKey map[string]*entry
	pq    *entryHeap
	cmps  int64 // unexported H-comparison counter, observed by tests
}

// New returns a Cache with the given byte capacity. Cap must be > 0.
func New(capacity int64) (*Cache, error) {
	if capacity <= 0 {
		return nil, ErrNonPositiveCapacity
	}
	c := &Cache{
		cap:   capacity,
		l:     new(big.Rat),
		byKey: make(map[string]*entry),
	}
	c.pq = &entryHeap{cmps: &c.cmps}
	return c, nil
}

// Put inserts or overwrites an artifact, evicting lowest-priority entries
// until it fits, and returns the evicted keys in eviction order.
//
// Validation rejects, in this order, reporting only the first failure:
// empty key, size <= 0, cost < 1, size > Cap. A rejected Put changes no
// entry, L, or tick. An overwrite removes the old entry first (freeing
// its bytes without advancing L) and then runs the plain insert flow.
// Eviction pops the minimum (H, last) entry and sets L to the victim's H,
// one victim at a time, until used+size <= Cap. No admission filtering is
// applied: the new entry is inserted even if its H is below existing ones.
func (c *Cache) Put(key string, size, cost int64) ([]string, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	if size <= 0 {
		return nil, ErrNonPositiveSize
	}
	if cost < 1 {
		return nil, ErrInvalidCost
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if size > c.cap {
		return nil, ErrSizeExceedsCapacity
	}
	if old, ok := c.byKey[key]; ok {
		heap.Remove(c.pq, old.index)
		delete(c.byKey, key)
		c.used -= old.size
	}
	var evicted []string
	for c.used+size > c.cap {
		victim := heap.Pop(c.pq).(*entry)
		delete(c.byKey, victim.key)
		c.used -= victim.size
		c.l = new(big.Rat).Set(victim.h)
		evicted = append(evicted, victim.key)
	}
	e := &entry{key: key, size: size, cost: cost, freq: 1}
	e.h = priority(c.l, e.freq, cost, size)
	e.last = c.tick
	c.tick++
	heap.Push(c.pq, e)
	c.byKey[key] = e
	c.used += size
	return evicted, nil
}

// Get records a cache hit. It reports whether the key was present.
// On a hit, freq is incremented, H is recomputed against the current L,
// last is stamped with the current tick, and tick advances. A miss (or an
// empty key, which is rejected) changes nothing.
func (c *Cache) Get(key string) (bool, error) {
	if key == "" {
		return false, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[key]
	if !ok {
		return false, nil
	}
	e.freq++
	e.h = priority(c.l, e.freq, e.cost, e.size)
	e.last = c.tick
	c.tick++
	heap.Fix(c.pq, e.index)
	return true, nil
}

// Peek returns a snapshot of the entry for key without mutating any state.
func (c *Cache) Peek(key string) (Info, error) {
	if key == "" {
		return Info{}, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[key]
	if !ok {
		return Info{}, ErrNotFound
	}
	return Info{Freq: e.freq, H: new(big.Rat).Set(e.h), Last: e.last}, nil
}

// Used returns the number of bytes currently cached.
func (c *Cache) Used() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

// L returns a copy of the current global inflation value.
func (c *Cache) L() *big.Rat {
	c.mu.Lock()
	defer c.mu.Unlock()
	return new(big.Rat).Set(c.l)
}

// Len returns the number of cached entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byKey)
}

// priority computes L + freq*cost/size with exact rational arithmetic.
// The product freq*cost is formed with big.Int so it cannot overflow.
func priority(l *big.Rat, freq, cost, size int64) *big.Rat {
	num := new(big.Int).Mul(big.NewInt(freq), big.NewInt(cost))
	h := new(big.Rat).SetFrac(num, big.NewInt(size))
	return h.Add(h, l)
}
