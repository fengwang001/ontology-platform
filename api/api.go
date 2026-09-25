// Package api is the public face of the slab allocator. It depends on
// package cache and re-exports its sentinel errors.
package api

import "ontology/cache"

// Sentinel errors; each rejected operation fails with exactly one of these.
var (
	ErrRawSize = cache.ErrRawSize // rawSize not positive
	ErrAlign   = cache.ErrAlign   // align not a power of two
	ErrTooBig  = cache.ErrTooBig  // aligned size exceeds slab size
	ErrBadFree = cache.ErrBadFree // offset not a live allocation
)

// Cache is a slab allocator for one fixed object size.
type Cache struct {
	c *cache.Cache
}

// NewCache builds a cache for objects of rawSize bytes, aligned to align
// (a power of two), packed into slabs of slabSize bytes.
func NewCache(rawSize, align, slabSize int) (*Cache, error) {
	c, err := cache.New(rawSize, align, slabSize)
	if err != nil {
		return nil, err
	}
	return &Cache{c: c}, nil
}

// Alloc returns the byte offset (identity) of a freshly allocated object.
func (a *Cache) Alloc() (int, error) { return a.c.Alloc() }

// Free returns the object at off; off must be a live allocation.
func (a *Cache) Free(off int) error { return a.c.Free(off) }

// Stats is a consistent snapshot of cache counters.
type Stats = cache.Stats

// Stats returns aggregate counters.
func (a *Cache) Stats() Stats { return a.c.Stats() }

// SelfCheck verifies the invariants on built-in operation sequences.
func SelfCheck() error { return cache.SelfCheck() }
