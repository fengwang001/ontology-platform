// Package ord defines the ordered-type constraint and re-exports the
// sentinel errors used by quickselect callers.
package ord

import (
	"cmp"

	"ontology/sel"
)

// Ordered is the set of types usable with KthSmallest.
type Ordered = cmp.Ordered

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrBadK  = sel.ErrBadK
	ErrEmpty = sel.ErrEmpty
)
