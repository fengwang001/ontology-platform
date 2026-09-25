// Package seg defines the index type used by callers and re-exports the
// sentinel errors of segtree so failures classify with errors.Is.
package seg

import "ontology/segtree"

// Index is a position in the underlying array.
type Index int

// Int converts an Index to a plain int for segtree calls.
func (i Index) Int() int { return int(i) }

// Range is a closed index interval [L, R].
type Range struct{ L, R Index }

// Sentinel errors, aliases of segtree's so errors.Is matches either way.
var (
	ErrBadRange    = segtree.ErrBadRange
	ErrOutOfBounds = segtree.ErrOutOfBounds
	ErrReversed    = segtree.ErrReversed
)
