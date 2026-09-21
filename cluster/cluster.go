// Package cluster is the gateway over replica logs and quorum logic.
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
	// FaultLag makes the replica stay arg entries behind.
	FaultLag
)

type faultSpec struct {
	f   Fault
	arg int
}

// Cluster gateways writes and consistent reads across replicas.
type Cluster struct {
	mu       sync.Mutex
	replicas []*replica.Replica
	faults   []faultSpec
	log      []replica.Entry // all proposed entries, contiguous from Index 1
	commit   uint64
	now      func() time.Time
	lastBeat time.Time
}

// New creates a cluster of n replicas. now supplies the current time.
func New(n int, now func() time.Time) *Cluster {
	c := &Cluster{now: now}
	for i := 0; i < n; i++ {
		c.replicas = append(c.replicas, replica.New(i))
		c.faults = append(c.faults, faultSpec{})
	}
	return c
}

// SetFault injects a fault on replica id.
func (c *Cluster) SetFault(id int, f Fault, arg int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.faults[id] = faultSpec{f: f, arg: arg}
}

// Commit returns the current commit point.
func (c *Cluster) Commit() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.commit
}
