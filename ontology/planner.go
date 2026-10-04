package ontology

import (
	"errors"
	"sync"
)

var (
	ErrBadBase      = errors.New("bad base snapshot")
	ErrBadLocal     = errors.New("bad local snapshot")
	ErrBadRemote    = errors.New("bad remote snapshot")
	ErrMergeInvalid = errors.New("invalid merged snapshot")
	ErrHasConflicts = errors.New("plan has conflicts")
)

type Snapshot map[int64]Entry

type Entry struct {
	Parent int64
	Name   string
	Dir    bool
	Hash   string
}

type Op int

const (
	Create Op = iota + 1
	Delete
	SetLoc
	SetHash
)

func (op Op) String() string {
	switch op {
	case Create:
		return "Create"
	case Delete:
		return "Delete"
	case SetLoc:
		return "SetLoc"
	case SetHash:
		return "SetHash"
	default:
		return "Unknown"
	}
}

type Action struct {
	Op     Op
	ID     int64
	Parent int64
	Name   string
	Dir    bool
	Hash   string
}

type ConflictKind int

const (
	DeleteModify ConflictKind = iota + 1
	Loc
	Content
)

func (kind ConflictKind) String() string {
	switch kind {
	case DeleteModify:
		return "DeleteModify"
	case Loc:
		return "Loc"
	case Content:
		return "Content"
	default:
		return "Unknown"
	}
}

type Conflict struct {
	ID   int64
	Kind ConflictKind
}

type PlanResult struct {
	ToLocal   []Action
	ToRemote  []Action
	Conflicts []Conflict
}

type Planner struct {
	base Snapshot
	mu   sync.RWMutex
}

func NewPlanner(base Snapshot) (*Planner, error) {
	if !validateSnapshot(base) {
		return nil, ErrBadBase
	}
	return &Planner{base: cloneSnapshot(base)}, nil
}

func (p *Planner) Plan(local, remote Snapshot) (PlanResult, error) {
	local = cloneSnapshot(local)
	remote = cloneSnapshot(remote)

	p.mu.RLock()
	result, err := p.planLocked(local, remote)
	p.mu.RUnlock()
	return result, err
}

func (p *Planner) Commit(local, remote Snapshot) (Snapshot, PlanResult, error) {
	local = cloneSnapshot(local)
	remote = cloneSnapshot(remote)

	p.mu.Lock()
	defer p.mu.Unlock()

	result, err := p.planLocked(local, remote)
	if err != nil {
		return nil, result, err
	}
	if len(result.Conflicts) != 0 {
		return nil, result, ErrHasConflicts
	}

	merged := applyActions(local, result.ToLocal)
	p.base = merged
	return cloneSnapshot(merged), result, nil
}

func (p *Planner) planLocked(local, remote Snapshot) (PlanResult, error) {
	if !validateSnapshot(local) {
		return PlanResult{}, ErrBadLocal
	}
	if !validateSnapshot(remote) {
		return PlanResult{}, ErrBadRemote
	}

	localOK, remoteOK := dirsAgree(p.base, local, remote)
	if !localOK {
		return PlanResult{}, ErrBadLocal
	}
	if !remoteOK {
		return PlanResult{}, ErrBadRemote
	}

	return buildPlan(p.base, local, remote)
}
