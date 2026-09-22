// Package version implements the append-only version chain of one key.
//
// A chain is a linked list ordered oldest -> newest. Every write appends a
// node while its transaction is active; the node becomes committed when the
// transaction commits and is detached when it rolls back. Visibility for a
// reader is decided by the reader's snapshot (see the Visibility interface),
// keeping this package free of any snapshot/registry dependency.
package version

import "ontology/txid"

// Kind distinguishes a value-bearing version from a delete marker.
type Kind uint8

const (
	// KindValue means the version stores a value.
	KindValue Kind = iota + 1
	// KindDelete means the version is a deletion tombstone.
	KindDelete
)

// State is the lifecycle phase of one version.
type State uint8

const (
	// StatePending: written by a transaction that has not committed yet.
	StatePending State = iota + 1
	// StateCommitted: writer committed at Commit.
	StateCommitted
	// StateAborted: writer rolled back; the node must be detached.
	StateAborted
)

// Version is one entry in a key's version chain.
type Version struct {
	// Writer is the transaction that produced this version.
	Writer txid.ID
	// Commit is the id at which the writer committed. It equals Writer in
	// this implementation (the id is allocated at begin and commit does not
	// renumber), and stays Invalid while pending.
	Commit txid.ID
	Kind   Kind
	State  State
	Value  []byte
	doomed bool
	prev   *Version
}

// IsDelete reports whether the version is a deletion tombstone.
func (v *Version) IsDelete() bool { return v.Kind == KindDelete }

// Visibility decides committed-version visibility for one reader. snapshot.S
// implements it; the interface lives here to keep the dependency direction
// version -> txid only.
type Visibility interface {
	// Visible reports whether a version committed at cid is visible.
	Visible(cid txid.ID) bool
	// IsSelf reports whether readerTxn is the reader's own write transaction
	// (so it can read its own uncommitted writes). Invalid means "pure
	// snapshot, no write transaction".
	IsSelf(readerTxn txid.ID) bool
	SelfTxn() txid.ID
}

// Outcome is the three-way result of looking a key up.
type Outcome int

const (
	// Absent: the key never existed as far as the reader can see.
	Absent Outcome = iota
	// Present: a value version is visible; Value holds a copy.
	Present
	// Deleted: a delete marker is the newest visible version.
	Deleted
)

// Result is one lookup outcome plus the value payload (Present only).
type Result struct {
	Outcome Outcome
	Value   []byte
}
