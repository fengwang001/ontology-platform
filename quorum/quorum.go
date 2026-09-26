// Package quorum decides majorities over a fixed set of acceptors.
// It depends only on package acpt. Chosen is answered from an incrementally
// maintained per-value accept-count table, never by scanning all acceptors.
package quorum

import (
	"sync/atomic"

	"ontology/acpt"
)

// Report is one acceptor's Prepare reply: its accepted proposal and value.
// AcceptedProposal == 0 means the acceptor has never accepted.
type Report struct {
	AcceptedProposal int
	AcceptedValue    int
}

// Cluster is n single-decree acceptors deciding one value, plus the
// bookkeeping needed for majority queries.
type Cluster struct {
	acceptors []acpt.Acceptor
	counts    map[int]int // value -> number of acceptors currently holding it
	majority  int
	// reads records how many acceptor states the last Chosen call inspected.
	// Deliberately unexported and atomic, because Chosen may run concurrently
	// under only a read lock. Chosen reads only counts, so it stores 0.
	reads atomic.Int64
}

// New builds a cluster of n acceptors; n must be positive and odd.
func New(n int) *Cluster {
	return &Cluster{
		acceptors: make([]acpt.Acceptor, n),
		counts:    make(map[int]int),
		majority:  n/2 + 1,
	}
}

// N reports the number of acceptors.
func (c *Cluster) N() int { return len(c.acceptors) }

// SnapshotAt returns (promised, acceptedProposal, acceptedValue) of acceptor acc.
func (c *Cluster) SnapshotAt(acc int) (int, int, int) {
	a := &c.acceptors[acc]
	return a.Promise(), a.Accepted(), a.AcceptedValue()
}

// Prepare forwards a prepare to acceptor acc and returns its reply.
func (c *Cluster) Prepare(acc, n int) (bool, Report) {
	ok, p, v := c.acceptors[acc].Prepare(n)
	return ok, Report{AcceptedProposal: p, AcceptedValue: v}
}

// Accept forwards an accept to acceptor acc and, on success, migrates the
// per-value accept counts (old value loses one vote, new value gains one).
// A refused accept changes neither acceptor state nor counts.
func (c *Cluster) Accept(acc, n, v int) bool {
	a := &c.acceptors[acc]
	oldP, oldV := a.Accepted(), a.AcceptedValue()
	if !a.Accept(n, v) {
		return false
	}
	if oldP > 0 {
		c.counts[oldV]--
		if c.counts[oldV] == 0 {
			delete(c.counts, oldV)
		}
	}
	c.counts[v]++
	return true
}

// Chosen returns the value currently held by a majority of acceptors.
// It consults only the incremental counts table and never reads acceptor
// state, so the internal read count does not grow with the number of acceptors.
func (c *Cluster) Chosen() (int, bool) {
	c.reads.Store(0) // only counts is consulted: zero acceptor states read
	for v, k := range c.counts {
		if k >= c.majority {
			return v, true
		}
	}
	return 0, false
}

// PickValue implements the proposer rule: reuse the value attached to the
// highest accepted proposal among a quorum of Prepare replies; only when none
// of the replies carried an accepted value may the proposer use its own.
func PickValue(reports []Report, own int) (value int, reused bool) {
	bestP, bestV := 0, 0
	for _, r := range reports {
		if r.AcceptedProposal > bestP {
			bestP, bestV = r.AcceptedProposal, r.AcceptedValue
		}
	}
	if bestP == 0 {
		return own, false
	}
	return bestV, true
}

// VerifyChosenReadBound reports whether Chosen's internal acceptor-read count
// stays within bound for clusters of every given (odd) size after one value is
// accepted everywhere. Only the verdict is exposed; the counter value is not.
func VerifyChosenReadBound(sizes []int, bound int) bool {
	for _, m := range sizes {
		c := New(m)
		for i := 0; i < m; i++ {
			c.acceptors[i].Prepare(1)
			if !c.Accept(i, 1, 42) {
				return false
			}
		}
		if v, ok := c.Chosen(); !ok || v != 42 || c.reads.Load() > int64(bound) {
			return false
		}
	}
	return true
}
