// Package reflog implements an object retention and reclamation
// subsystem driven by per-reference append-only logs (reflogs).
//
// Every move of a reference appends a log record. Unexpired records
// keep the commits they mention alive; a garbage collector reclaims
// commits and content objects that are neither reachable from live
// roots nor protected by the freshness grace window.
package reflog

import "errors"

// Error values, listed in the mandated precedence order. Each call
// reports at most one error: the earliest applicable one in this list.
var (
	ErrInvalidParam    = errors.New("reflog: invalid parameter")
	ErrClockRegression = errors.New("reflog: clock regression")
	ErrRefNotFound     = errors.New("reflog: reference not found")
	ErrCommitNotFound  = errors.New("reflog: commit not found")
	ErrRecordNotFound  = errors.New("reflog: record not found")
)

// errPrecedence is the declared error ordering, used by tests to
// verify adjacent-pair precedence that no single call can trigger.
var errPrecedence = []error{
	ErrInvalidParam,
	ErrClockRegression,
	ErrRefNotFound,
	ErrCommitNotFound,
	ErrRecordNotFound,
}

// CommitID uniquely identifies a commit. The empty string means "no
// commit" (the old value of a creation record, the new value of a
// deletion record).
type CommitID string

// ObjectID uniquely identifies a content object.
type ObjectID string

// Commit is an immutable node in the object graph.
type Commit struct {
	ID        CommitID
	Parents   []CommitID
	CreatedAt int64
	Contents  []ObjectID
	Size      int64
	// FirstWrittenAt is the local time at which the commit was first
	// written to this store.
	FirstWrittenAt int64
}

// Object is a content object referenced by commits.
type Object struct {
	ID   ObjectID
	Size int64
	// FirstWrittenAt is the local time at which the object was first
	// written to this store.
	FirstWrittenAt int64
}

// Record is one reflog entry. Old is empty for creation, New is empty
// for deletion. Seq orders records that share the same Time.
type Record struct {
	Seq  uint64
	Old  CommitID
	New  CommitID
	Time int64
	Who  string
}

// Config holds the retention knobs.
type Config struct {
	// ReachableRetention applies to records whose old value is an
	// ancestor of (or equal to) the reference's current value.
	ReachableRetention int64
	// UnreachableRetention applies to all other records, and to every
	// record of a deleted reference.
	UnreachableRetention int64
	// FreshnessGrace protects recently written objects from
	// reclamation.
	FreshnessGrace int64
}

// GCStats reports the outcome of one reclamation pass.
type GCStats struct {
	Commits int
	Objects int
	Bytes   int64
}
