// Package cluster is a gateway over a set of replica logs. It assigns
// indexes, replicates entries to every reachable replica, decides the
// commit point by majority, and serves consistent reads.
//
// Dependency direction: cluster -> quorum and cluster -> replica only.
package cluster

import (
	"sync"
	"time"

	"ontology/replica"
)

// Fault is an injected failure mode for a single replica.
type Fault int

const (
	// FaultNone means the replica behaves normally.
	FaultNone Fault = iota
	// FaultReject makes the replica reject appends with an error.
	FaultReject
	// FaultDrop makes the replica silently discard appends.
	FaultDrop
	// FaultLag keeps the replica arg entries behind the latest index.
	FaultLag
)

// Cluster coordinates n replicas behind a single gateway. The zero value
// is not usable; call New. All methods are safe for concurrent use.
type Cluster struct {
	mu       sync.Mutex
	now      func() time.Time
	reps     []*replica.Replica
	faults   []Fault
	faultArg []int
	log      []replica.Entry // gateway log; log[k] has Index k+1
	commit   uint64
	commitAt time.Time
}

// New creates a cluster of n replicas. now supplies the clock used to
// timestamp commit advancements; nil means time.Now.
func New(n int, now func() time.Time) *Cluster {
	if now == nil {
		now = time.Now
	}
	c := &Cluster{
		now:      now,
		faults:   make([]Fault, n),
		faultArg: make([]int, n),
	}
	for i := 0; i < n; i++ {
		c.reps = append(c.reps, replica.New(i))
	}
	return c
}

// SetFault injects a fault mode on replica id. arg only matters for
// FaultLag, where it is the number of entries the replica lags behind.
func (c *Cluster) SetFault(id int, f Fault, arg int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.faults[id] = f
	c.faultArg[id] = arg
}
