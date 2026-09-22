// Package version implements per-key version chains.
//
// Every write to a key appends a new version to that key's chain. The chain
// is ordered oldest-to-newest, where "newer" means appended by a later
// transaction. A reader walks from the tip backwards and stops at the first
// version visible through its snapshot: that is the value the key held at the
// reader's snapshot instant.
//
// package snapshot defines the visibility test; taking snapshot as an
// argument keeps the dependency one-directional (version must not import
// snapshot, because snapshot knows nothing about chains).
package version

import (
	"errors"

	"ontology/txid"
)

// ErrChainLimit is returned when appending would exceed a key's configured
// maximum retained version count.
var ErrChainLimit = errors.New("version: per-key version chain limit reached")

// V is one version of one key.
type V struct {
	// Creator is the transaction that wrote the version.
	Creator txid.TxID
	// Deleted marks a tombstone: the key was deleted in this version.
	Deleted bool
	// Value is the payload; ignored when Deleted is true.
	Value []byte
}

// VisibleBy is the visibility predicate injected by the store. It reports
// whether the writing transaction of a version is visible to a reader.
type VisibleBy func(creator txid.TxID) bool

// Result is what a snapshot read of one key yields.
type Result struct {
	// Found distinguishes "key exists" from both "never existed" and
	// "deleted".
	Found bool
	// Deleted is true only when the visible version is a tombstone.
	Deleted bool
	// Value holds a copy of the visible payload when Found && !Deleted.
	Value []byte
}

// Outcome of an append, used by the reclaimer to learn which previous tip
// became shadowed.
type Outcome struct {
	// Shadowed is the version that was the chain tip before this append,
	// or Zero when the chain was empty (nothing became a candidate).
	Shadowed txid.TxID
	// NewTip is the creator of the freshly appended version.
	NewTip txid.TxID
}
