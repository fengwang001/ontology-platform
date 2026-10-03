// Package triplesync implements a three-way directory snapshot sync planner
// keyed by stable file ids.
package triplesync

import (
	"errors"
	"sync"
)

// Entry is a single node in a snapshot. Parent 0 means the root.
type Entry struct {
	Parent int64
	Name   string
	Dir    bool
	Hash   string
}

// Snapshot maps stable id (>= 1) to its entry.
type Snapshot map[int64]Entry

// Snapshot validation and merge failures.
var (
	ErrBadBase      = errors.New("triplesync: invalid base snapshot")
	ErrBadLocal     = errors.New("triplesync: invalid local snapshot")
	ErrBadRemote    = errors.New("triplesync: invalid remote snapshot")
	ErrMergeInvalid = errors.New("triplesync: merged snapshot would be invalid")
	ErrHasConflicts = errors.New("triplesync: plan has unresolved conflicts")
)

// Action kinds emitted in a plan.
type ActionKind int

const (
	ActionCreate ActionKind = iota + 1
	ActionSetLoc
	ActionSetHash
	ActionDelete
)

// Action is one instruction applied to one side's snapshot.
type Action struct {
	Kind   ActionKind
	ID     int64
	Parent int64
	Name   string
	Dir    bool
	Hash   string
}

// Conflict kinds, in per-id reporting order.
type ConflictKind int

const (
	ConflictDeleteModify ConflictKind = iota + 1
	ConflictLoc
	ConflictContent
)

// Conflict describes one conflicting attribute (or delete/modify) for an id.
type Conflict struct {
	ID   int64
	Kind ConflictKind
}

// Plan is the result of a three-way comparison.
type Plan struct {
	ToLocal   []Action
	ToRemote  []Action
	Conflicts []Conflict
}

// Planner holds an immutable base snapshot.
type Planner struct {
	base Snapshot
	mu   sync.Mutex
}
