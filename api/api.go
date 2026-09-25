// Package api is the public facade over the slab cache. It exposes only
// NewCache/Alloc/Free/Stats/SelfCheck and the decidable sentinel errors;
// the non-exported complexity counter never appears here.
package api

import "ontology/cache"

// Cache is the public fixed-object-size slab cache (alias of the concrete
// cache.Cache, so Alloc/Free/Stats/SelfCheck are promoted unchanged).
type Cache = cache.Cache

// Stats re-exports the consistent statistics snapshot.
type Stats = cache.Stats

// Four mutually distinguishable sentinel errors.
var (
	ErrBadSize     = cache.ErrBadSize
	ErrBadAlign    = cache.ErrBadAlign
	ErrDoesNotFit  = cache.ErrDoesNotFit
	ErrInvalidFree = cache.ErrInvalidFree
)

// NewCache builds a cache for one fixed aligned object size.
func NewCache(rawSize, align, slabSize int) (*Cache, error) {
	return cache.NewCache(rawSize, align, slabSize)
}
