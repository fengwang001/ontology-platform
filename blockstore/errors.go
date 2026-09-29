// Package blockstore implements a content-addressed, deduplicating block
// store with a two-phase (mark/sweep) garbage collector that is safe under
// concurrent backup writers.
package blockstore

import "errors"

// Distinguishable rejection reasons returned by Upload / Commit.
var (
	// ErrCapacityFull is returned when storing new data would exceed the
	// configured capacity.
	ErrCapacityFull = errors.New("blockstore: storage capacity full")
	// ErrSessionNotFound is returned when the referenced session does not
	// exist (already ended, swept, or never opened).
	ErrSessionNotFound = errors.New("blockstore: session not found or already ended")
	// ErrDuplicateCommit is returned when a session is committed a second
	// time. The first commit (or none) remains the only state change.
	ErrDuplicateCommit = errors.New("blockstore: session already committed")
	// ErrBlockMissing is returned when a manifest references a digest that
	// was never uploaded and does not exist in the store.
	ErrBlockMissing = errors.New("blockstore: manifest references a non-existent block")
	// ErrNoGCCycle is returned when Sweep is called without a matching Mark,
	// or after that cycle has already been swept.
	ErrNoGCCycle = errors.New("blockstore: no active two-phase cycle to sweep")
	// ErrBlockDeleted is returned when reading a digest whose block has been
	// physically deleted.
	ErrBlockDeleted = errors.New("blockstore: block has been deleted")
)
