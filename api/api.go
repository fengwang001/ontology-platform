// Package api is the external surface of the aligned allocator. It depends
// on package alloc and exposes nothing beyond it.
package api

import "ontology/alloc"

// Allocator is the aligned bump allocator. Obtain one with New.
// Its complexity counter is an unexported field of the underlying
// implementation and is not reachable through this interface.
type Allocator = alloc.Allocator

// The three distinguishable rejection kinds.
var (
	ErrInvalidSize  = alloc.ErrInvalidSize
	ErrInvalidAlign = alloc.ErrInvalidAlign
	ErrBadFree      = alloc.ErrBadFree
)

// New returns an empty allocator with bump pointer next = 0.
func New() *Allocator {
	return alloc.New()
}
