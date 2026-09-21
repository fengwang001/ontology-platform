package cluster

import (
	"ontology/quorum"
	"ontology/replica"
)

// Propose assigns the next index to data, replicates it (plus any
// backlog) to every reachable replica, and advances the commit point to
// the highest index matched by a majority. It reports whether the new
// entry itself is committed when it returns.
func (c *Cluster) Propose(term uint64, data string) (index uint64, committed bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	index = uint64(len(c.log)) + 1
	c.log = append(c.log, replica.Entry{Index: index, Term: term, Data: data})

	matches := make([]uint64, len(c.reps))
	for id := range c.reps {
		c.syncReplica(id)
		matches[id] = c.reps[id].Match()
	}
	if ci := quorum.CommitIndex(matches, len(c.reps)); ci > c.commit {
		c.commit = ci
		c.commitAt = c.now()
	}
	return index, c.commit >= index, nil
}

// syncReplica brings replica id up to date, honouring its fault mode.
// Conflicting tails are truncated before re-appending. Callers must
// hold c.mu.
func (c *Cluster) syncReplica(id int) {
	rep := c.reps[id]
	latest := uint64(len(c.log))
	limit := latest
	switch c.faults[id] {
	case FaultReject, FaultDrop:
		return
	case FaultLag:
		backlog := uint64(c.faultArg[id])
		if backlog >= latest {
			return
		}
		limit = latest - backlog
	}
	for i := rep.Match() + 1; i <= limit; i++ {
		want := c.log[i-1]
		if have, ok := rep.Get(i); ok && have != want {
			rep.Truncate(i)
		}
		if err := rep.Append(want); err != nil {
			rep.Truncate(i)
			if err := rep.Append(want); err != nil {
				return
			}
		}
	}
}
