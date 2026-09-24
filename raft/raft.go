// Package raft holds per-replica state: term-anchored log replication
// with follower truncation, and quorum commit limited to the current term.
package raft

import (
	"errors"
	"sync"

	"ontology/entry"
)

// Distinct sentinel errors; callers discriminate with errors.Is.
var ErrEmptyCmd = errors.New("raft: empty command")
var ErrPrevIndexOutOfRange = errors.New("raft: prevIndex out of range")
var ErrPrevTermMismatch = errors.New("raft: prevTerm mismatch")
var ErrUnknownServer = errors.New("raft: unknown server")

type Replica struct {
	term        int
	log         *entry.Log
	commitIndex int // per-replica: a crashed leader's point is not shared
}

// Cluster is the fixed replica set; one mutex guards every transition
// and snapshot.
type Cluster struct {
	mu    sync.Mutex
	order []string
	rs    map[string]*Replica
	// scanned: entries inspected by the last CommitIndex; unexported,
	// read only by the white-box test here.
	scanned int
}

func NewCluster(servers ...string) *Cluster {
	c := &Cluster{rs: map[string]*Replica{}}
	for _, s := range servers {
		c.rs[s] = &Replica{log: entry.NewLog()}
		c.order = append(c.order, s)
	}
	return c
}

func (c *Cluster) get(s string) (*Replica, error) {
	if r, ok := c.rs[s]; ok {
		return r, nil
	}
	return nil, ErrUnknownServer
}

// SetTerm records a scenario-driven election result.
func (c *Cluster) SetTerm(server string, term int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.get(server)
	if err != nil {
		return err
	}
	r.term = term
	return nil
}

// Append appends cmd at the server's current term; empty cmd is rejected
// before touching any state.
func (c *Cluster) Append(server, cmd string) (entry.Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cmd == "" {
		return entry.Entry{}, ErrEmptyCmd
	}
	r, err := c.get(server)
	if err != nil {
		return entry.Entry{}, err
	}
	e := entry.Entry{Term: r.term, Index: r.log.Len() + 1, Cmd: cmd}
	r.log.Append(e)
	return e, nil
}

// Replicate ships leader's suffix after prevIndex with anchor
// (prevIndex, leader term there): range/term failure leaves the follower
// untouched, a match truncates the conflicting suffix and replaces it.
func (c *Cluster) Replicate(leader, follower string, prevIndex int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok1 := c.rs[leader]
	f, ok2 := c.rs[follower]
	if !ok1 || !ok2 {
		return ErrUnknownServer
	}
	// A positive anchor must exist on both logs; prevIndex 0 is empty.
	if prevIndex < 0 || prevIndex > l.log.Len() || prevIndex > f.log.Len() {
		return ErrPrevIndexOutOfRange
	}
	prevTerm := 0
	if prevIndex > 0 {
		a, _ := l.log.At(prevIndex)
		prevTerm = a.Term
	}
	if !f.log.PrefixMatch(prevIndex, prevTerm) { // all checks precede mutation
		return ErrPrevTermMismatch
	}
	f.log.Truncate(prevIndex)
	for _, e := range l.log.After(prevIndex) {
		f.log.Append(e)
	}
	return nil
}

// CommitIndex resumes just past the previous point, so only new entries
// are inspected. i commits iff same-term copies are on a quorum AND
// term == leader term; old terms are skipped, a short quorum blocks.
func (c *Cluster) CommitIndex(leader string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.get(leader)
	if err != nil {
		return 0, err
	}
	c.scanned = 0
	for i := r.commitIndex + 1; i <= r.log.Len(); i++ {
		c.scanned++
		le, _ := r.log.At(i)
		if le.Term != r.term {
			continue
		}
		votes := 0
		for _, n := range c.order {
			if x, ok := c.rs[n].log.At(i); ok && x.Term == le.Term {
				votes++
			}
		}
		if votes < len(c.order)/2+1 {
			break
		}
		r.commitIndex = i
	}
	return r.commitIndex, nil
}

// Log returns a defensive snapshot of the server's log.
func (c *Cluster) Log(server string) ([]entry.Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.get(server)
	if err != nil {
		return nil, err
	}
	return r.log.Clone(), nil
}
