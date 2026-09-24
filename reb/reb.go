// Package reb implements the two-round incremental cooperative rebalance.
package reb

import (
	"errors"
	"slices"

	"ontology/ring"
)

var (
	ErrBadParam  = errors.New("reb: id or partition out of range")
	ErrDuplicate = errors.New("reb: member already joined")
	ErrNoMember  = errors.New("reb: no such member")
	ErrNoRevoke  = errors.New("reb: no pending revocation")
)

// State is the lifecycle state of a partition.
type State int

const (
	Unowned   State = iota // no holder
	Consuming              // held and being consumed
	Revoking               // still held, consumption stopped, awaiting ack
)

// Partition is a snapshot row: holder (-1 when unowned) and state.
type Partition struct {
	Owner int
	State State
}

// Rebalancer holds all partition/member state in memory.
type Rebalancer struct {
	P        int
	members  ring.Set
	parts    []Partition          // owner (-1 if unowned) and state per partition
	held     map[int]map[int]bool // member -> all partitions it holds
	rev      map[int]map[int]bool // member -> its revoking partitions
	revoking int                  // group-wide revoking count
	pending  []int                // unowned partitions awaiting round two
	checked  int                  // partitions inspected by the last Join/Leave/RevokeAck
}

func New(P int) (*Rebalancer, error) {
	if P <= 0 {
		return nil, ErrBadParam
	}
	r := &Rebalancer{P: P, parts: slices.Repeat([]Partition{{-1, Unowned}}, P),
		held: map[int]map[int]bool{}, rev: map[int]map[int]bool{}, pending: make([]int, P)}
	for p := range r.pending {
		r.pending[p] = p
	}
	return r, nil
}

// Join adds member x and runs round one (revoke) plus maybe round two.
func (r *Rebalancer) Join(x int) error {
	r.checked = 0
	if x < 0 || x >= r.P {
		return ErrBadParam
	}
	if !r.members.Add(x) {
		return ErrDuplicate
	}
	r.held[x], r.rev[x] = map[int]bool{}, map[int]bool{}
	// Only partitions whose target became x can need revocation.
	r.members.Range(x, r.P, func(p int) {
		r.checked++
		if r.parts[p].State == Consuming {
			r.parts[p].State = Revoking
			r.revoking++
			r.rev[r.parts[p].Owner][p] = true
		}
	})
	r.assign()
	return nil
}

// Leave removes member x; its partitions become unowned immediately.
func (r *Rebalancer) Leave(x int) error {
	r.checked = 0
	if x < 0 || x >= r.P {
		return ErrBadParam
	}
	if !r.members.Has(x) {
		return ErrNoMember
	}
	for p := range r.held[x] {
		r.checked++
		r.drop(p)
	}
	r.members.Remove(x)
	delete(r.held, x)
	delete(r.rev, x)
	r.assign()
	return nil
}

// RevokeAck confirms all of x's revoking partitions; they become unowned.
func (r *Rebalancer) RevokeAck(x int) error {
	r.checked = 0
	if x < 0 || x >= r.P {
		return ErrBadParam
	}
	if !r.members.Has(x) {
		return ErrNoMember
	}
	if len(r.rev[x]) == 0 {
		return ErrNoRevoke
	}
	for p := range r.rev[x] {
		r.checked++
		delete(r.held[x], p)
		r.drop(p)
	}
	r.rev[x] = map[int]bool{} // x stays a member; future joins may revoke to it
	r.assign()
	return nil
}

func (r *Rebalancer) drop(p int) {
	if r.parts[p].State == Revoking {
		r.revoking--
	}
	r.parts[p] = Partition{-1, Unowned}
	r.pending = append(r.pending, p)
}

func (r *Rebalancer) assign() {
	if r.revoking > 0 || r.members.Len() == 0 {
		return
	}
	for _, p := range r.pending {
		r.checked++
		t, _ := r.members.Target(p)
		r.parts[p] = Partition{t, Consuming}
		r.held[t][p] = true
	}
	r.pending = r.pending[:0]
}

func (r *Rebalancer) Owner(p int) (int, State, error) {
	if p < 0 || p >= r.P {
		return 0, 0, ErrBadParam
	}
	return r.parts[p].Owner, r.parts[p].State, nil
}

func (r *Rebalancer) Snapshot() []Partition { return slices.Clone(r.parts) }
