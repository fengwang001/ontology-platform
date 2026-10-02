// Package epaxos implements an EPaxos-style committed-instance execution
// scheduler. It orders committed instances for execution by strongly
// connected components (SCCs) of the dependency graph: dependencies are
// executed before their dependents, and instances inside one component are
// ordered by (seq, replica, slot).
package epaxos

import (
	"fmt"
	"sort"
	"sync"
)

// Instance identifies a committed command instance by replica and slot.
// R must be in [0, N) and I must be >= 1.
type Instance struct {
	R int
	I int
}

// ErrCode distinguishes the reasons a Commit (or construction) can fail.
type ErrCode int

const (
	// ErrInvalidConfig: N or Cap was not positive at construction.
	ErrInvalidConfig ErrCode = iota
	// ErrInvalidInstance: the instance or one of its deps has R out of
	// range or I < 1.
	ErrInvalidInstance
	// ErrInvalidSeq: seq was not positive.
	ErrInvalidSeq
	// ErrSelfDependency: deps contain the instance itself.
	ErrSelfDependency
	// ErrDuplicateDependency: deps contain duplicates.
	ErrDuplicateDependency
	// ErrConflictingCommit: the instance was already registered with a
	// different seq or dependency set.
	ErrConflictingCommit
	// ErrCapacityExceeded: registering a new instance would exceed Cap
	// unexecuted committed instances.
	ErrCapacityExceeded
)

// Error describes a rejected operation. Code identifies the reason.
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Scheduler orders committed instances for execution. All methods are safe
// for concurrent use; results are equivalent to some serial order.
type Scheduler struct {
	mu      sync.Mutex
	n       int
	cap     int
	records map[Instance]*record
	pending int // committed but not yet executed
}

type record struct {
	seq      int
	deps     []Instance // canonical: sorted by (R, I), deduplicated
	depSet   map[Instance]struct{}
	executed bool
}

// New creates a Scheduler for n replicas holding at most cap unexecuted
// committed instances. Both must be positive.
func New(n, cap int) (*Scheduler, error) {
	if n <= 0 || cap <= 0 {
		return nil, newError(ErrInvalidConfig, "epaxos: N and Cap must be positive, got N=%d Cap=%d", n, cap)
	}
	return &Scheduler{
		n:       n,
		cap:     cap,
		records: make(map[Instance]*record),
	}, nil
}

// Commit registers a committed instance. See package docs for validation
// order; a rejected commit changes no state.
func (s *Scheduler) Commit(inst Instance, seq int, deps []Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitLocked(inst, seq, deps)
}

// Execute runs all executable committed instances in deterministic order and
// returns the newly executed instances. Blocked instances stay pending.
func (s *Scheduler) Execute() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.executeLocked()
}

// Pending returns the committed-but-unexecuted instances sorted by (R, I).
func (s *Scheduler) Pending() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Instance, 0, s.pending)
	for inst, rec := range s.records {
		if !rec.executed {
			out = append(out, inst)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].R != out[b].R {
			return out[a].R < out[b].R
		}
		return out[a].I < out[b].I
	})
	return out
}
