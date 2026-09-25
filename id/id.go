// Package id provides the element index type used across the ontology
// equivalence layer and re-exports the sentinel errors raised by uf.
package id

import "ontology/uf"

// Index identifies an instance element within a fixed union-find universe.
type Index int

// Sentinel errors are the same values produced by the uf package so callers
// can distinguish the three failure classes with errors.Is.
var (
	ErrNegativeSize = uf.ErrNegativeSize
	ErrBadIndex     = uf.ErrBadIndex
	ErrNilReceiver  = uf.ErrNilReceiver
)

// Int converts an index to the integer representation consumed by uf.
func (x Index) Int() int { return int(x) }
