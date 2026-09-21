package cluster

import (
	"fmt"

	"ontology/quorum"
	"ontology/replica"
)

// Propose appends data at the next Index and replicates it.
// It reports whether the entry reached the commit point.
func (c *Cluster) Propose(term uint64, data string) (index uint64, committed bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := len(c.log); n > 0 && term < c.log[n-1].Term {
		return 0, false, fmt.Errorf("cluster: term %d regresses below %d", term, c.log[n-1].Term)
	}
	index = uint64(len(c.log)) + 1
	c.log = append(c.log, replica.Entry{Index: index, Term: term, Data: data})
	c.replicateLocked()
	c.advanceCommitLocked()
	c.lastBeat = c.now()
	return index, c.commit >= index, nil
}

// replicateLocked repairs divergence and delivers pending entries.
func (c *Cluster) replicateLocked() {
	for id := range c.replicas {
		c.repairLocked(id)
		target := uint64(len(c.log))
		if f := c.faults[id]; f.f == FaultLag && f.arg > 0 {
			if uint64(f.arg) < target {
				target -= uint64(f.arg)
			} else {
				target = 0
			}
		}
		r := c.replicas[id]
		for i := r.Match() + 1; i <= target; i++ {
			c.deliverLocked(id, c.log[i-1])
		}
	}
}

// repairLocked truncates the uncommitted divergent tail of replica id.
func (c *Cluster) repairLocked(id int) {
	r := c.replicas[id]
	limit := r.Match()
	if uint64(len(c.log)) < limit {
		limit = uint64(len(c.log))
	}
	for i := c.commit + 1; i <= limit; i++ {
		have, _ := r.Get(i)
		want := c.log[i-1]
		if have.Term != want.Term || have.Data != want.Data {
			r.Truncate(i)
			return
		}
	}
}

// deliverLocked sends entry e to replica id, honoring its fault.
func (c *Cluster) deliverLocked(id int, e replica.Entry) {
	switch c.faults[id].f {
	case FaultReject, FaultDrop:
		return
	default:
		_ = c.replicas[id].Append(e)
	}
}

// advanceCommitLocked moves the commit point to the majority match.
func (c *Cluster) advanceCommitLocked() {
	matches := make([]uint64, len(c.replicas))
	for i, r := range c.replicas {
		matches[i] = r.Match()
	}
	if next := quorum.CommitIndex(matches, len(c.replicas)); next > c.commit {
		c.commit = next
	}
}
