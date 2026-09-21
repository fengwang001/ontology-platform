// Package cluster is the gateway over replica logs with quorum commits.
package cluster

import (
	"sync"
	"time"

	"ontology/replica"
)

// Fault is an injectable failure mode for a replica.
type Fault int

const (
	// FaultNone means the replica behaves normally.
	FaultNone Fault = iota
	// FaultReject makes the replica reject appends.
	FaultReject
	// FaultDrop makes the replica silently drop appends.
	FaultDrop
	// FaultLag makes the replica lag arg entries behind; later
	// proposals backfill the missing entries.
	FaultLag
)

type faultSpec struct {
	kind Fault
	arg  int
}

// Cluster manages n replicas and a quorum-based commit point.
// It is safe for concurrent use.
type Cluster struct {
	mu       sync.Mutex
	n        int
	replicas []*replica.Replica
	faults   []faultSpec
	log      []replica.Entry
	commit   uint64
	lastTerm uint64
	now      func() time.Time
}

// New creates a Cluster of n replicas. now supplies the clock; a nil
// clock falls back to time.Now.
func New(n int, now func() time.Time) *Cluster {
	if n < 1 {
		n = 1
	}
	if now == nil {
		now = time.Now
	}
	c := &Cluster{n: n, now: now}
	c.replicas = make([]*replica.Replica, n)
	c.faults = make([]faultSpec, n)
	for i := range c.replicas {
		c.replicas[i] = replica.New(i)
	}
	return c
}

// SetFault injects a fault on replica id. arg only matters for FaultLag.
func (c *Cluster) SetFault(id int, f Fault, arg int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id < 0 || id >= c.n {
		return
	}
	c.faults[id] = faultSpec{kind: f, arg: arg}
}

// Commit returns the current commit index. It never decreases.
func (c *Cluster) Commit() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commit
}
