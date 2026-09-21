package cluster

import (
	"errors"

	"ontology/quorum"
)

// ErrNoQuorum is returned by ReadIndex when fewer than a majority of
// replicas are reachable, so no fresh read index can be certified.
var ErrNoQuorum = errors.New("cluster: no quorum reachable")

// Commit returns the current commit index. It never decreases.
func (c *Cluster) Commit() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commit
}

// ReadIndex returns a read index that is at least the current commit
// point, but only after confirming that a majority of replicas is still
// reachable. Without a majority it returns ErrNoQuorum instead of a
// possibly stale index.
func (c *Cluster) ReadIndex() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reachable := 0
	for id := range c.reps {
		if c.faults[id] != FaultReject && c.faults[id] != FaultDrop {
			reachable++
		}
	}
	if reachable < quorum.Majority(len(c.reps)) {
		return 0, ErrNoQuorum
	}
	return c.commit, nil
}

// Snapshot returns the data of the committed entries (Index <= Commit)
// persisted on replica id, ordered by ascending index.
func (c *Cluster) Snapshot(id int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for i := uint64(1); i <= c.commit; i++ {
		if e, ok := c.reps[id].Get(i); ok {
			out = append(out, e.Data)
		}
	}
	return out
}
