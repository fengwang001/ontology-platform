package cluster

import (
	"errors"
	"fmt"

	"ontology/quorum"
	"ontology/replica"
)

// Propose appends data at the next index and replicates it. It reports
// the assigned index and whether the entry is committed (acknowledged
// by a majority) when the call returns.
func (c *Cluster) Propose(term uint64, data string) (index uint64, committed bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if term < c.lastTerm {
		return 0, false, fmt.Errorf("cluster: term %d regresses below %d", term, c.lastTerm)
	}
	c.lastTerm = term
	idx := uint64(len(c.log)) + 1
	c.log = append(c.log, replica.Entry{Index: idx, Term: term, Data: data})
	for id := 0; id < c.n; id++ {
		c.replicateLocked(id, idx)
	}
	matches := make([]uint64, c.n)
	for id := 0; id < c.n; id++ {
		matches[id] = c.replicas[id].Match()
	}
	if ci := quorum.CommitIndex(matches, c.n); ci > c.commit {
		c.commit = ci
	}
	return idx, c.commit >= idx, nil
}

// replicateLocked delivers log entries to replica id, honoring its fault.
func (c *Cluster) replicateLocked(id int, idx uint64) {
	f := c.faults[id]
	switch f.kind {
	case FaultReject, FaultDrop:
		return
	case FaultLag:
		limit := uint64(0)
		if arg := f.arg; arg > 0 && idx > uint64(arg) {
			limit = idx - uint64(arg)
		}
		c.catchUpLocked(id, limit)
	default:
		c.catchUpLocked(id, idx)
	}
}

// catchUpLocked appends every missing log entry up to limit, keeping the
// replica contiguous.
func (c *Cluster) catchUpLocked(id int, limit uint64) {
	r := c.replicas[id]
	if limit > uint64(len(c.log)) {
		limit = uint64(len(c.log))
	}
	for i := r.Match() + 1; i <= limit; i++ {
		if err := r.Append(c.log[i-1]); err != nil {
			return
		}
	}
}

// ReadIndex returns a consistent read index. It only succeeds while a
// majority of replicas is still reachable, so the returned index never
// reflects a stale, minority view.
func (c *Cluster) ReadIndex() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reachable := 0
	for id := 0; id < c.n; id++ {
		if k := c.faults[id].kind; k != FaultReject && k != FaultDrop {
			reachable++
		}
	}
	if reachable < 1 {
		return 0, errors.New("cluster: no majority reachable")
	}
	return c.commit, nil
}

// Snapshot returns the committed data stored on replica id, ordered by
// index. A lagging replica simply reports a shorter prefix.
func (c *Cluster) Snapshot(id int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id < 0 || id >= c.n {
		return nil
	}
	r := c.replicas[id]
	limit := c.commit
	if m := r.Match(); m < limit {
		limit = m
	}
	out := make([]string, 0, limit)
	for i := uint64(1); i <= limit; i++ {
		if e, ok := r.Get(i); ok {
			out = append(out, e.Data)
		}
	}
	return out
}
