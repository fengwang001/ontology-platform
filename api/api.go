// Package api is the public facade of the consistent-hash ring.
package api

import (
	"errors"
	"fmt"
	"sort"

	"ontology/hashk"
	"ontology/ring"
)

// Sentinel errors: the four failure modes are distinct and decidable.
var (
	ErrEmptyRing     = errors.New("ring: get on empty ring")
	ErrNodeExists    = errors.New("ring: node already exists")
	ErrNodeNotFound  = errors.New("ring: node not found")
	ErrInvalidVnodes = errors.New("ring: vnodes must be >= 1")
)

// Ring is the exported, concurrency-safe ring handle.
type Ring struct {
	inner *ring.Ring
}

// New builds a ring with vnodes virtual nodes per node. vnodes < 1 is rejected.
func New(vnodes int) (*Ring, error) {
	if vnodes < 1 {
		return nil, ErrInvalidVnodes
	}
	return &Ring{inner: ring.New(vnodes)}, nil
}

// AddNode validates before mutating: a duplicate id fails, leaves no trace.
func (r *Ring) AddNode(id uint32) error {
	if !r.inner.Add(id) { // Add rejects internally without mutating
		return ErrNodeExists
	}
	return nil
}

// RemoveNode fails on a non-member without touching ring contents.
func (r *Ring) RemoveNode(id uint32) error {
	if !r.inner.Remove(id) {
		return ErrNodeNotFound
	}
	return nil
}

// Get returns the owner of key, or ErrEmptyRing when there are no nodes.
func (r *Ring) Get(key uint32) (uint32, error) {
	owner, ok := r.inner.Get(key)
	if !ok {
		return 0, ErrEmptyRing
	}
	return owner, nil
}

// NodeCount returns the number of attached nodes.
func (r *Ring) NodeCount() int { return r.inner.NodeCount() }

// naiveOwner is an independent linear-scan reference built straight from hashk.
func naiveOwner(vnodes int, members []uint32, key uint32) uint32 {
	type slot struct {
		pos  uint32
		node uint32
	}
	var ss []slot
	for _, id := range members {
		for i := 0; i < vnodes; i++ {
			ss = append(ss, slot{hashk.VNodePos(id, i), id})
		}
	}
	sort.Slice(ss, func(a, b int) bool { return ss[a].pos < ss[b].pos })
	h := hashk.H(key)
	for _, s := range ss {
		if s.pos >= h { // >= : equal position belongs to that vnode
			return s.node
		}
	}
	return ss[0].node // wrap-around
}

// SelfCheck verifies the four invariants over built-in nodes 1/2/3 and keys.
func (r *Ring) SelfCheck() error {
	v, nodes, keys := 2, []uint32{1, 2, 3}, []uint32{10, 20, 30, 40, 50, 60, 70, 80}
	c, err := New(v)
	if err != nil {
		return err
	}
	for _, id := range nodes {
		if err := c.AddNode(id); err != nil {
			return err
		}
	}
	member := map[uint32]bool{1: true, 2: true, 3: true}
	// Inv1 ownership determined; Inv2 equals the naive reference.
	for _, k := range keys {
		got, err := c.Get(k)
		if err != nil || !member[got] {
			return fmt.Errorf("inv1 key %d: owner %d err %v", k, got, err)
		}
		if want := naiveOwner(v, nodes, k); got != want {
			return fmt.Errorf("inv2 key %d: got %d want %d", k, got, want)
		}
	}
	// Inv3 removal consistency: node 3 never owns any key afterward.
	if err := c.RemoveNode(3); err != nil {
		return err
	}
	want := map[uint32]uint32{30: 1, 70: 2, 80: 1}
	for k, w := range want {
		if got, _ := c.Get(k); got != w {
			return fmt.Errorf("inv3 key %d: got %d want %d", k, got, w)
		}
	}
	for k := uint32(0); k < 5000; k++ {
		if got, _ := c.Get(k); got == 3 {
			return fmt.Errorf("inv3 key %d still owned by removed node", k)
		}
	}
	return checkRejectionsLeaveNoTrace()
}

// checkRejectionsLeaveNoTrace verifies Inv4: the four distinct sentinel
// errors, state unchanged after each rejection, ring still usable.
func checkRejectionsLeaveNoTrace() error {
	if _, err := New(0); !errors.Is(err, ErrInvalidVnodes) {
		return fmt.Errorf("inv4 invalid vnodes: %v", err)
	}
	e, _ := New(2)
	if _, err := e.Get(1); !errors.Is(err, ErrEmptyRing) {
		return fmt.Errorf("inv4 empty get: %v", err)
	}
	if err := e.AddNode(1); err != nil {
		return err
	}
	if err := e.AddNode(1); !errors.Is(err, ErrNodeExists) || e.NodeCount() != 1 {
		return fmt.Errorf("inv4 duplicate add: %v count %d", err, e.NodeCount())
	}
	if err := e.RemoveNode(9); !errors.Is(err, ErrNodeNotFound) || e.NodeCount() != 1 {
		return fmt.Errorf("inv4 missing remove: %v count %d", err, e.NodeCount())
	}
	if err := e.AddNode(2); err != nil { // still usable after rejections
		return fmt.Errorf("inv4 ring unusable after rejection: %v", err)
	}
	return nil
}
