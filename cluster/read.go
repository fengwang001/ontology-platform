package cluster

import (
	"errors"

	"ontology/quorum"
)

// ErrNoQuorum is returned when a majority is not reachable.
var ErrNoQuorum = errors.New("cluster: no quorum reachable")

// ReadIndex returns a consistent read index confirmed by a majority.
func (c *Cluster) ReadIndex() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reachable := 0
	for _, f := range c.faults {
		if f.f == FaultNone || f.f == FaultLag {
			reachable++
		}
	}
	if reachable < quorum.Majority(len(c.replicas)) {
		return 0, ErrNoQuorum
	}
	c.lastBeat = c.now()
	return c.commit, nil
}

// Snapshot returns committed data of replica id, ordered by Index.
func (c *Cluster) Snapshot(id int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.replicas[id]
	limit := c.commit
	if r.Match() < limit {
		limit = r.Match()
	}
	out := make([]string, 0, limit)
	for i := uint64(1); i <= limit; i++ {
		if e, ok := r.Get(i); ok {
			out = append(out, e.Data)
		}
	}
	return out
}
