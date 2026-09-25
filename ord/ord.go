// Package ord exposes the comparable-type constraint used by sel and
// re-exports its sentinel errors so callers can match them with errors.Is.
package ord

import (
	"cmp"

	"ontology/sel"
)

// Ordered is the constraint of types usable with sel.KthSmallest.
type Ordered = cmp.Ordered

// Sentinel errors returned by sel.KthSmallest; identical to sel's values,
// so errors.Is works against either package's names.
var (
	ErrEmpty = sel.ErrEmpty
	ErrBadK  = sel.ErrBadK
)
