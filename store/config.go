// Package store ties txid, version, snapshot and reclaim together into an
// in-process multi-version key store: append-only writes, snapshot reads,
// incremental reclamation, resource limits and crash/restart recovery.
//
// All state lives in process memory. Time is never used; transaction ids
// come only from an injected txid.Source.
package store

import "errors"

import "ontology/txid"

// Config sets the three independently enforceable resource limits.
// A zero/negative value means "unlimited" for that limit.
type Config struct {
	// MaxVersionsPerKey bounds one key's chain length (pending included).
	MaxVersionsPerKey int
	// MaxActiveSnapshots bounds simultaneously open read snapshots.
	MaxActiveSnapshots int
	// MaxTotalVersions bounds versions attached across all keys.
	MaxTotalVersions int
}

// The three limit errors are distinct sentinels so callers can tell the
// rejected resource apart with errors.Is.
var (
	// ErrVersionLimitForKey: a single key's chain is at MaxVersionsPerKey.
	ErrVersionLimitForKey = errors.New("store: per-key version chain limit reached")
	// ErrSnapshotLimit: MaxActiveSnapshots open snapshots already.
	ErrSnapshotLimit = errors.New("store: active snapshot limit reached")
	// ErrTotalVersionLimit: MaxTotalVersions versions already attached.
	ErrTotalVersionLimit = errors.New("store: total version limit reached")
)

// State errors, also distinguishable by identity.
var (
	errTxnClosed = errors.New("store: transaction already finished")
	errReadOnly  = errors.New("store: read view is read only")
)

// Option customizes a store at construction.
type Option func(*Store)

// WithSource injects an external transaction-id source. Without one the
// store creates an internal in-process source (still "injected" in the sense
// that no other id source exists in the codebase).
func WithSource(src IDSource) Option {
	return func(s *Store) { s.ids = src }
}

// IDSource is an alias of txid.Source so callers inject the id stream.
type IDSource = txid.Source
