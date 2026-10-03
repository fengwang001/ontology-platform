// Package syncplan implements a three-way directory sync planner keyed by
// stable file ids. It compares a base snapshot with a local and a remote
// snapshot and produces deterministic action lists for both directions,
// a conflict list, and (via Commit) a new base snapshot.
package syncplan

import (
	"errors"
	"fmt"
)

var (
	// ErrBadBase is returned by NewPlanner when the base snapshot is invalid.
	ErrBadBase = errors.New("syncplan: invalid base snapshot")
	// ErrBadLocal is returned when the local snapshot is invalid on its own
	// or inconsistent with the base snapshot. It takes precedence over
	// ErrBadRemote and ErrMergeInvalid.
	ErrBadLocal = errors.New("syncplan: invalid local snapshot")
	// ErrBadRemote is returned when the remote snapshot is invalid on its
	// own or inconsistent with the base/local snapshots.
	ErrBadRemote = errors.New("syncplan: invalid remote snapshot")
	// ErrMergeInvalid is returned when a post-merge state would contain a
	// cycle, a dangling parent, or duplicate names under one parent.
	ErrMergeInvalid = errors.New("syncplan: merged state is invalid")
	// ErrHasConflicts is returned by Commit when the plan has conflicts.
	ErrHasConflicts = errors.New("syncplan: plan has conflicts")
)

// Entry is a single file or directory in a snapshot.
type Entry struct {
	Parent int    // 0 means root, otherwise the id of a directory entry
	Name   string // non-empty, must not contain '/'
	Dir    bool
	Hash   string // non-empty for files, must be empty for directories
}

// Snapshot maps a stable id (>= 1) to an entry.
type Snapshot map[int]Entry

// ActionKind identifies the kind of a sync action.
type ActionKind int

const (
	ActionCreate ActionKind = iota
	ActionSetLoc
	ActionSetHash
	ActionDelete
)

func (k ActionKind) String() string {
	switch k {
	case ActionCreate:
		return "Create"
	case ActionSetLoc:
		return "SetLoc"
	case ActionSetHash:
		return "SetHash"
	case ActionDelete:
		return "Delete"
	}
	return fmt.Sprintf("ActionKind(%d)", int(k))
}

// Action is a single mutation applied to one side's snapshot.
// Create uses Parent/Name/Dir/Hash, SetLoc uses Parent/Name,
// SetHash uses Hash, Delete uses only ID.
type Action struct {
	Kind   ActionKind
	ID     int
	Parent int
	Name   string
	Dir    bool
	Hash   string
}

func (a Action) String() string {
	switch a.Kind {
	case ActionCreate:
		return fmt.Sprintf("Create#%d(parent=%d,name=%q,dir=%v,hash=%q)", a.ID, a.Parent, a.Name, a.Dir, a.Hash)
	case ActionSetLoc:
		return fmt.Sprintf("SetLoc#%d(parent=%d,name=%q)", a.ID, a.Parent, a.Name)
	case ActionSetHash:
		return fmt.Sprintf("SetHash#%d(hash=%q)", a.ID, a.Hash)
	case ActionDelete:
		return fmt.Sprintf("Delete#%d", a.ID)
	}
	return fmt.Sprintf("Action{%v #%d}", a.Kind, a.ID)
}

// ConflictKind identifies the kind of a three-way conflict.
type ConflictKind int

const (
	ConflictDeleteModify ConflictKind = iota
	ConflictLoc
	ConflictContent
)

func (k ConflictKind) String() string {
	switch k {
	case ConflictDeleteModify:
		return "DeleteModify"
	case ConflictLoc:
		return "Loc"
	case ConflictContent:
		return "Content"
	}
	return fmt.Sprintf("ConflictKind(%d)", int(k))
}

// Conflict records one conflicting attribute of one id.
type Conflict struct {
	ID   int
	Kind ConflictKind
}

func (c Conflict) String() string {
	return fmt.Sprintf("#%d %v", c.ID, c.Kind)
}

// Plan is the result of a three-way comparison. ToLocal applies to the
// local snapshot, ToRemote applies to the remote snapshot.
type Plan struct {
	ToLocal   []Action
	ToRemote  []Action
	Conflicts []Conflict
}

func (p Plan) String() string {
	return fmt.Sprintf("Plan{ToLocal=%v, ToRemote=%v, Conflicts=%v}", p.ToLocal, p.ToRemote, p.Conflicts)
}

func cloneSnapshot(s Snapshot) Snapshot {
	out := make(Snapshot, len(s))
	for id, e := range s {
		out[id] = e
	}
	return out
}
