// Package ttlcache implements an LRU cache with per-item TTL.
// Time is fully injected by the caller; the cache never reads a clock itself.
package ttlcache

import (
	"container/list"
	"errors"
	"sync"
)

var (
	// ErrInvalidCapacity is returned by New when capacity <= 0.
	ErrInvalidCapacity = errors.New("ttlcache: capacity must be positive")
	// ErrInvalidTTL is returned by Put when ttlMillis <= 0.
	ErrInvalidTTL = errors.New("ttlcache: ttlMillis must be positive")
)

type entry struct {
	key       string
	val       string
	createdAt int64 // logical write time in millis
	expireAt  int64 // createdAt + ttl; expired when now >= expireAt
}

// Cache is a fixed-capacity LRU cache with per-item TTL.
// It is safe for concurrent use.
type Cache struct {
	mu       sync.Mutex
	capacity int
	now      func() int64
	ll       *list.List // front = most recently used
	items    map[string]*list.Element
}

// New creates a Cache with the given capacity. now supplies the current
// logical time in milliseconds and must be non-nil.
func New(capacity int, now func() int64) (*Cache, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if now == nil {
		now = func() int64 { return 0 }
	}
	return &Cache{
		capacity: capacity,
		now:      now,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
	}, nil
}

// expired reports whether e is expired at logical time now.
// Expiry is "expire at the deadline": now == expireAt is already expired.
func expired(e *entry, now int64) bool {
	return now >= e.expireAt
}

// remove unlinks elem from the list and the map.
func (c *Cache) remove(elem *list.Element) {
	c.ll.Remove(elem)
	delete(c.items, elem.Value.(*entry).key)
}
