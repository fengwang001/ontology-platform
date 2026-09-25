// Package api is the public facade over the alloc package: New, Alloc, Free,
// Allocated and SelfCheck are the only entry points users need.
package api

import (
	"ontology/align"
	"ontology/alloc"
)

// The three distinct, decidable sentinel errors. A rejected operation leaves
// no state behind, and the allocator stays usable afterwards.
var (
	ErrBadSize     = align.ErrBadSize  // size was not >= 1
	ErrBadAlign    = align.ErrBadAlign // align was not a power of two >= 1
	ErrInvalidFree = alloc.ErrInvalidFree
)

// Allocator is a concurrency-safe linear allocator in an abstract byte space.
type Allocator struct {
	a *alloc.Allocator
}

// New returns an allocator whose bump pointer starts at zero.
func New() *Allocator {
	return &Allocator{a: alloc.New()}
}

// Alloc returns an align-aligned pointer to a size-byte reservation, with an
// 8-byte header storing the reservation base directly before the pointer.
func (x *Allocator) Alloc(size, al int) (ptr int, err error) {
	return x.a.Alloc(size, al)
}

// Free releases ptr (which must be live) after reading its base back from the
// header at [ptr-headerSize, ptr). Released space is never reused.
func (x *Allocator) Free(ptr int) error {
	_, err := x.a.Free(ptr)
	return err
}

// Allocated reports the number of currently live allocations.
func (x *Allocator) Allocated() int { return x.a.Len() }

// SelfCheck runs the built-in sequence and the four invariant checks against a
// naive simulation; it inspects only fresh internal state.
func (x *Allocator) SelfCheck() error { return x.a.SelfCheck() }
