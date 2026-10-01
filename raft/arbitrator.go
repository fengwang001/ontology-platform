// Package raft implements a commit arbitrator for Raft joint-consensus
// membership changes. The arbitrator tracks per-node match indexes and
// advances a commit index according to quorum rules of the active
// configuration, which is either a single node set or a joint
// configuration (old, new).
package raft

import (
	"errors"
	"sort"
	"sync"
)

// Distinguishable rejection reasons returned by Arbitrator operations.
var (
	ErrUnknownNode      = errors.New("raft: ack from unknown node")
	ErrNotSingleConfig  = errors.New("raft: not in single configuration")
	ErrEmptySet         = errors.New("raft: node set is empty")
	ErrDuplicateNode    = errors.New("raft: node set contains duplicate node")
	ErrEmptyNodeID      = errors.New("raft: node set contains empty node id")
	ErrSameSet          = errors.New("raft: new set identical to current set")
	ErrIdxNotAfterCfg   = errors.New("raft: idx not greater than cfgIdx")
	ErrNotJointConfig   = errors.New("raft: not in joint configuration")
	ErrIdxNotAfterJoint = errors.New("raft: idx not greater than jointIdx")
	ErrCommitBelowJoint = errors.New("raft: commit index below jointIdx")
)

// Arbitrator arbitrates the commit index across single and joint
// configurations. All methods are safe for concurrent use; concurrent
// calls behave as if executed in some serial order.
type Arbitrator struct {
	mu sync.Mutex

	joint bool
	// cur is the active single set, or the old set while joint.
	cur map[string]struct{}
	// next is the new set while joint; nil otherwise.
	next map[string]struct{}

	cfgIdx   uint64
	jointIdx uint64
	commit   uint64
	match    map[string]uint64
}

// NewArbitrator builds an arbitrator in a single configuration over
// nodes. The set must be non-empty, free of duplicates and of empty
// identifiers. cfgIdx and commit start at 0 and every node's match
// index starts at 0.
func NewArbitrator(nodes []string) (*Arbitrator, error) {
	set, err := validateSet(nodes)
	if err != nil {
		return nil, err
	}
	match := make(map[string]uint64, len(set))
	for id := range set {
		match[id] = 0
	}
	return &Arbitrator{cur: set, match: match}, nil
}

// Ack records that node has replicated up to idx, keeping the maximum
// seen so far, then recomputes and returns the commit index. An idx
// below the recorded match is not an error.
func (a *Arbitrator) Ack(node string, idx uint64) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.match[node]
	if !ok {
		return 0, ErrUnknownNode
	}
	if idx > m {
		a.match[node] = idx
	}
	a.recomputeLocked()
	return a.commit, nil
}

// BeginJoint switches from the single configuration to the joint
// configuration (cur, newSet), effective immediately. Nodes of newSet
// not previously known become known with match 0. The joint entry is
// recorded at index idx, which must be greater than cfgIdx.
func (a *Arbitrator) BeginJoint(newSet []string, idx uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.joint {
		return ErrNotSingleConfig
	}
	set, err := validateSet(newSet)
	if err != nil {
		return err
	}
	if equalSet(a.cur, set) {
		return ErrSameSet
	}
	if idx <= a.cfgIdx {
		return ErrIdxNotAfterCfg
	}
	a.joint = true
	a.next = set
	a.jointIdx = idx
	for id := range set {
		if _, ok := a.match[id]; !ok {
			a.match[id] = 0
		}
	}
	a.recomputeLocked()
	return nil
}

// FinishJoint leaves the joint configuration: the new set becomes the
// single configuration at cfgIdx = idx, effective immediately, and
// nodes outside it are forgotten. It requires idx > jointIdx and
// commit >= jointIdx.
func (a *Arbitrator) FinishJoint(idx uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.joint {
		return ErrNotJointConfig
	}
	if idx <= a.jointIdx {
		return ErrIdxNotAfterJoint
	}
	if a.commit < a.jointIdx {
		return ErrCommitBelowJoint
	}
	a.joint = false
	a.cur = a.next
	a.next = nil
	a.cfgIdx = idx
	for id := range a.match {
		if _, ok := a.cur[id]; !ok {
			delete(a.match, id)
		}
	}
	a.recomputeLocked()
	return nil
}

// Commit returns the current commit index.
func (a *Arbitrator) Commit() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.commit
}

// recomputeLocked raises commit to the largest index committable under
// the active configuration. commit never decreases.
func (a *Arbitrator) recomputeLocked() {
	candidate := quorumIndex(a.cur, a.match)
	if a.joint {
		if n := quorumIndex(a.next, a.match); n < candidate {
			candidate = n
		}
	}
	if candidate > a.commit {
		a.commit = candidate
	}
}

// quorumIndex returns the largest N such that at least ⌊size/2⌋+1
// members of set have match >= N.
func quorumIndex(set map[string]struct{}, match map[string]uint64) uint64 {
	values := make([]uint64, 0, len(set))
	for id := range set {
		values = append(values, match[id])
	}
	sort.Slice(values, func(i, j int) bool { return values[i] > values[j] })
	return values[len(values)/2]
}

func validateSet(nodes []string) (map[string]struct{}, error) {
	if len(nodes) == 0 {
		return nil, ErrEmptySet
	}
	set := make(map[string]struct{}, len(nodes))
	for _, id := range nodes {
		if _, dup := set[id]; dup {
			return nil, ErrDuplicateNode
		}
		set[id] = struct{}{}
	}
	for id := range set {
		if id == "" {
			return nil, ErrEmptyNodeID
		}
	}
	return set, nil
}

func equalSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			return false
		}
	}
	return true
}
