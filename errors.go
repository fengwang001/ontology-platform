package ontology

import "errors"

// Sentinel errors. Use errors.Is to classify failures.
var (
	// ErrNeedsRebalance reports that generating a key between the given
	// neighbors would exceed the configured maximum key length. The
	// sequence must be rebalanced before more inserts at that spot.
	ErrNeedsRebalance = errors.New("sort key length limit reached: rebalance required")

	// ErrInvalidOrder reports neighbors that are not strictly ordered
	// (left >= right). This is a caller bug, distinct from
	// ErrNeedsRebalance.
	ErrInvalidOrder = errors.New("invalid neighbors: left key must be strictly less than right key")

	// ErrInvalidChar reports a key containing a byte outside the allowed
	// charset. The wrapped message names the offending character.
	ErrInvalidChar = errors.New("invalid character in sort key")

	// ErrDuplicateValue reports an insert whose value already occupies
	// the target gap, so no deterministic tie-break exists.
	ErrDuplicateValue = errors.New("duplicate value in sequence")

	// ErrNoRoom reports a neighbor pair with no key in between, e.g.
	// right == left followed only by reserved firstChar bytes. This is
	// a caller bug: generated keys never create such pairs.
	ErrNoRoom = errors.New("no sort key exists between the given neighbors")
)
