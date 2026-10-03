// Package roadnet implements a time-dependent road network with
// announcement history and earliest-arrival queries.
//
// Edge travel costs vary with the departure time according to a
// piecewise profile. New profiles can be announced over time and old
// announcements can be replaced; every accepted mutation bumps a
// version counter so that historical queries (EarliestArrival with an
// explicit version) are exactly reproducible.
package roadnet

import (
	"errors"
	"fmt"
	"sync"
)

// Limits imposed by the service contract.
const (
	MaxNodes    = 5000
	MaxEdges    = 100000
	MaxTime     = int64(1_000_000_000)
	ClosedCost  = int64(-1)
	MaxCost     = int64(1_000_000)
	MaxSegments = 32
	MaxRecords  = 64
)

// Distinguishable rejection/failure reasons. Every fallible method
// returns an error wrapping exactly one of these sentinels (test with
// errors.Is), and checks them in the order mandated by the contract.
var (
	// ErrInvalidParam: constructor arguments out of range, unknown
	// node, time/version/profile out of range, u == v, etc.
	ErrInvalidParam = errors.New("roadnet: invalid parameter")
	// ErrEdgeLimit: the edge id space (E) is exhausted.
	ErrEdgeLimit = errors.New("roadnet: edge limit reached")
	// ErrEdgeNotFound: Announce referenced an edge id never assigned.
	ErrEdgeNotFound = errors.New("roadnet: edge does not exist")
	// ErrRetroactive: Announce effective time is earlier than now.
	ErrRetroactive = errors.New("roadnet: announcement rewrites the past")
	// ErrOutOfOrder: effective time is earlier than the edge's last record.
	ErrOutOfOrder = errors.New("roadnet: announcement effective time out of order")
	// ErrRecordLimit: the edge already has MaxRecords records.
	ErrRecordLimit = errors.New("roadnet: record limit reached")
	// ErrClockBack: Advance was given a time earlier than now.
	ErrClockBack = errors.New("roadnet: clock rollback")
	// ErrUnreachable: no s->g path exists at the queried version.
	ErrUnreachable = errors.New("roadnet: goal unreachable")
	// ErrVersionFuture: queried version has not been produced yet.
	ErrVersionFuture = errors.New("roadnet: version not yet produced")
)

// Segment is one piece of a travel-cost profile: starting Offset
// (relative to the record's effective time) the edge costs Cost to
// traverse, until the next segment starts. Cost == ClosedCost (-1)
// means the edge is closed and no traversal may start inside the
// segment. The last segment extends to infinity.
type Segment struct {
	Offset int64
	Cost   int64
}

// record is one announcement on an edge.
type record struct {
	eff     int64
	profile []Segment
	regVer  int64 // version that registered this record
	replVer int64 // version that replaced it; 0 while still live
}

// visibleAt reports whether the record is visible at version ver.
func (r *record) visibleAt(ver int64) bool {
	return r.regVer <= ver && (r.replVer == 0 || r.replVer > ver)
}

// edge is a directed edge; parallel edges are distinct entries.
type edge struct {
	id      int
	u, v    int
	regVer  int64
	records []record // insertion order; live records have non-decreasing eff
}

// lastLive returns the edge's last live record (records are never
// removed, only marked replaced, and the tail is always live).
func (e *edge) lastLive() *record {
	for i := len(e.records) - 1; i >= 0; i-- {
		if e.records[i].replVer == 0 {
			return &e.records[i]
		}
	}
	return nil
}

// liveCount counts the records not replaced so far.
func (e *edge) liveCount() int {
	n := 0
	for i := range e.records {
		if e.records[i].replVer == 0 {
			n++
		}
	}
	return n
}

// Net is a time-dependent road network. All methods are safe for
// concurrent use; results are equivalent to some serial order.
type Net struct {
	mu      sync.RWMutex
	n       int
	edgeCap int
	now     int64
	version int64
	edges   []*edge
	adj     [][]int // adj[u] = ids (1-based) of edges leaving u
}

// NewNet creates a network with n nodes (0..n-1) and room for edgeCap
// edges. The clock starts at 0 and the version at 0.
func NewNet(n, edgeCap int) (*Net, error) {
	if n < 1 || n > MaxNodes {
		return nil, fmt.Errorf("%w: node count %d outside [1,%d]", ErrInvalidParam, n, MaxNodes)
	}
	if edgeCap < 1 || edgeCap > MaxEdges {
		return nil, fmt.Errorf("%w: edge cap %d outside [1,%d]", ErrInvalidParam, edgeCap, MaxEdges)
	}
	return &Net{
		n:       n,
		edgeCap: edgeCap,
		adj:     make([][]int, n),
	}, nil
}

// Now returns the current system clock.
func (nt *Net) Now() int64 {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	return nt.now
}

// Version returns the current network version.
func (nt *Net) Version() int64 {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	return nt.version
}

// EdgeCount returns the number of edges registered so far.
func (nt *Net) EdgeCount() int {
	nt.mu.RLock()
	defer nt.mu.RUnlock()
	return len(nt.edges)
}

// validNode reports whether x is a node of the network.
func (nt *Net) validNode(x int) bool { return x >= 0 && x < nt.n }

// validTime reports whether t is inside [0, MaxTime].
func validTime(t int64) bool { return t >= 0 && t <= MaxTime }

// validProfile checks the profile contract: 1..MaxSegments segments,
// first offset 0, offsets strictly increasing inside [0, MaxTime],
// costs in [1, MaxCost] or ClosedCost.
func validProfile(p []Segment) bool {
	if len(p) < 1 || len(p) > MaxSegments {
		return false
	}
	if p[0].Offset != 0 {
		return false
	}
	for i, s := range p {
		if s.Offset < 0 || s.Offset > MaxTime {
			return false
		}
		if i > 0 && s.Offset <= p[i-1].Offset {
			return false
		}
		if s.Cost != ClosedCost && (s.Cost < 1 || s.Cost > MaxCost) {
			return false
		}
	}
	return true
}

// cloneProfile copies a profile so later caller mutations cannot
// rewrite history.
func cloneProfile(p []Segment) []Segment {
	q := make([]Segment, len(p))
	copy(q, p)
	return q
}
