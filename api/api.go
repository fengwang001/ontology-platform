// Package api is the outward-facing wrapper over package repl.
package api

import (
	"ontology/repl"
)

// Re-exported sentinel errors so callers never import repl directly.
var (
	ErrFollowerIndex = repl.ErrFollowerIndex
	ErrTermMismatch  = repl.ErrTermMismatch
	ErrStaleTerm     = repl.ErrStaleTerm
	ErrHintRange     = repl.ErrHintRange
)

// Cluster is the leader's replication bookkeeping for an n-node cluster.
type Cluster struct {
	l *repl.Leader
}

// New creates a cluster of n (odd) nodes. Node 1 is the leader; the cluster
// starts at term 0, so call Elect before Append.
func New(n int) *Cluster {
	return &Cluster{l: repl.New(n)}
}

// Append appends a log entry of the given term (must equal the current term).
func (c *Cluster) Append(term int) error { return c.l.Append(term) }

// Replicate records follower f's response: ok with last index r, or a
// rejection hinting its last index r.
func (c *Cluster) Replicate(f int, ok bool, r int) error { return c.l.Replicate(f, ok, r) }

// Elect re-elects the leader in term t (must exceed the current term).
func (c *Cluster) Elect(t int) error { return c.l.Elect(t) }

// CommitIndex returns the highest committed index (monotonically non-decreasing).
func (c *Cluster) CommitIndex() int { return c.l.CommitIndex() }

// MatchIndex returns the known replicated prefix length of follower f.
func (c *Cluster) MatchIndex(f int) int { return c.l.MatchIndex(f) }

// NextIndex returns the next log index to send to follower f.
func (c *Cluster) NextIndex(f int) int { return c.l.NextIndex(f) }

// SelfCheck replays built-in scenarios against a naive oracle and verifies
// all four bookkeeping invariants. It returns nil on success.
func (c *Cluster) SelfCheck() error { return repl.SelfCheck() }
