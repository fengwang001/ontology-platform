// Package itc implements a named-replica registry of Interval Tree Clocks,
// following the formal definition of Interval Tree Clocks.
//
// A stamp is a pair (identity; event). Both components are binary trees:
//
//	identity: 0 | 1 | (left, right)
//	event:    n | (n, left, right), n a non-negative integer
//
// Trees are kept in their normalized form.
package itc

import (
	"errors"
	"sync"
)

// Errors returned by Registry operations. Sentinel errors are used so callers
// can distinguish each rejection reason with errors.Is.
var (
	ErrEmptyName       = errors.New("itc: name must not be empty")
	ErrNameExists      = errors.New("itc: name already exists")
	ErrUnknownName     = errors.New("itc: unknown replica name")
	ErrSameName        = errors.New("itc: both names are the same")
	ErrIdentityOverlap = errors.New("itc: identities overlap")
)

// ID is a normalized identity tree.
type ID struct {
	kind  uint8 // 0: zero leaf, 1: one leaf, 2: pair
	left  *ID
	right *ID
}

// Event is a normalized event tree.
type Event struct {
	kind  uint8 // 0: integer leaf n, 1: node (n,left,right)
	n     int
	left  *Event
	right *Event
}

// Relation is the outcome of comparing two event trees.
type Relation int

const (
	// Equal means the two events are causally identical.
	Equal Relation = iota
	// Before means a's events are strictly before b's.
	Before
	// After means a's events are strictly after b's.
	After
	// Concurrent means neither event is less-or-equal to the other.
	Concurrent
)

// Registry holds named replicas. Its methods are safe for concurrent use and
// appear to execute in some serial order.
type Registry struct {
	mu       sync.Mutex
	replicas map[string]stamp
}

type stamp struct {
	id    ID
	event Event
}

// The concrete registry operations are implemented in registry.go.
