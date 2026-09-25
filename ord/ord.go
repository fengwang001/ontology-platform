// Package ord defines the ordered-type constraint and re-exports the
// sentinel errors of the selection packages.
package ord

import (
	"cmp"

	"ontology/sel"
)

// Ordered is the set of types supporting the operators < <= >= >.
type Ordered = cmp.Ordered

// Sentinel errors returned by sel.KthSmallest, re-exported so callers
// can classify failures with errors.Is against ord.ErrEmpty / ord.ErrBadK.
var (
	ErrEmpty = sel.ErrEmpty
	ErrBadK  = sel.ErrBadK
)
