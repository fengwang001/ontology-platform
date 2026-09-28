// Package counter implements a conflict-free replicated PN-counter.
//
// Each replica keeps two non-negative vectors (increments P and
// decrements N) indexed by replica id. Local updates only touch the
// replica's own component; a merge takes the component-wise maximum of
// both sides into the destination replica.
package counter

import (
	"math/big"
	"sync"
)

// MaxComponent is the exclusive upper bound of every P/N component.
const MaxComponent = 1 << 62

// Errors returned for rejected operations. Each rejected call is atomic:
// no replica state is modified before an error is returned.
var (
	errInvalidReplica   = newCounterError("counter: replica id is out of range")
	errNonPositive      = newCounterError("counter: delta must be positive")
	errComponentLimit   = newCounterError("counter: component would exceed limit")
	errNilSnapshot      = newCounterError("counter: nil snapshot argument")
	errSnapshotMismatch = newCounterError("counter: snapshot width does not match cluster")
)

// CounterError carries a machine-readable reason kind.
type CounterError struct {
	kind string
	msg  string
}

func newCounterError(msg string) *CounterError {
	return &CounterError{kind: msg, msg: msg}
}

func (e *CounterError) Error() string { return e.msg }

// Re-exported sentinel errors for errors.Is comparisons.
var (
	ErrInvalidReplica   = errInvalidReplica
	ErrNonPositiveDelta = errNonPositive
	ErrComponentLimit   = errComponentLimit
	ErrNilSnapshot      = errNilSnapshot
	ErrSnapshotMismatch = errSnapshotMismatch
	ErrInvalidCluster   = errInvalidCluster
	ErrCorruptSnapshot  = errCorruptSnapshot
	ErrCorruptState     = errCorruptState
)

var (
	errInvalidCluster  = newCounterError("counter: cluster size must be positive")
	errCorruptSnapshot = newCounterError("counter: snapshot contains negative or over-limit components")
	errCorruptState    = newCounterError("counter: replica state failed self check")
)

// Snapshot is an immutable point-in-time copy of one replica's vectors.
type Snapshot struct {
	id int
	p  []*big.Int
	n  []*big.Int
}

// Cluster is a fixed-size group of replicas.
type Cluster struct {
	n        int
	replicas []*replica
}

type replica struct {
	mu sync.Mutex
	p  []*big.Int
	n  []*big.Int
}

// NewCluster creates a cluster with n replicas numbered 0..n-1.
func NewCluster(n int) (*Cluster, error) {
	if n <= 0 {
		return nil, ErrInvalidCluster
	}
	c := &Cluster{n: n, replicas: make([]*replica, n)}
	for i := range c.replicas {
		r := &replica{
			p: make([]*big.Int, n),
			n: make([]*big.Int, n),
		}
		for j := 0; j < n; j++ {
			r.p[j] = new(big.Int)
			r.n[j] = new(big.Int)
		}
		c.replicas[i] = r
	}
	return c, nil
}

// Inc adds delta to replica id's increment component.
func (c *Cluster) Inc(id int, delta uint64) error {
	return c.update(id, delta, true)
}

// Dec adds delta to replica id's decrement component.
func (c *Cluster) Dec(id int, delta uint64) error {
	return c.update(id, delta, false)
}

// Merge merges replica src into replica dst.
func (c *Cluster) Merge(dst, src int) error {
	if !c.validID(dst) || !c.validID(src) {
		return ErrInvalidReplica
	}
	if dst == src {
		// Merging a replica into itself is a legal no-op.
		return nil
	}
	a, b := c.lockPair(dst, src)
	defer a.Unlock()
	if a != b {
		defer b.Unlock()
	}
	dr, sr := c.replicas[dst], c.replicas[src]
	for i := 0; i < c.n; i++ {
		if sr.p[i].Cmp(dr.p[i]) > 0 {
			dr.p[i].Set(sr.p[i])
		}
		if sr.n[i].Cmp(dr.n[i]) > 0 {
			dr.n[i].Set(sr.n[i])
		}
	}
	return nil
}

// Value returns sum(P) - sum(N) for replica id.
func (c *Cluster) Value(id int) (*big.Int, error) {
	if !c.validID(id) {
		return nil, ErrInvalidReplica
	}
	r := c.replicas[id]
	r.mu.Lock()
	defer r.mu.Unlock()
	v := new(big.Int)
	for i := 0; i < c.n; i++ {
		v.Add(v, r.p[i])
		v.Sub(v, r.n[i])
	}
	return v, nil
}

// SnapshotAt returns a detached copy of replica id's state.
func (c *Cluster) SnapshotAt(id int) (*Snapshot, error) {
	if !c.validID(id) {
		return nil, ErrInvalidReplica
	}
	r := c.replicas[id]
	r.mu.Lock()
	defer r.mu.Unlock()
	return newSnapshot(id, r.p, r.n), nil
}

// InstallSnapshot replaces replica id's state with the given snapshot.
func (c *Cluster) InstallSnapshot(id int, snap *Snapshot) error {
	if !c.validID(id) {
		return ErrInvalidReplica
	}
	if snap == nil {
		return ErrNilSnapshot
	}
	if len(snap.p) != c.n || len(snap.n) != c.n {
		return ErrSnapshotMismatch
	}
	cp := make([]*big.Int, c.n)
	cn := make([]*big.Int, c.n)
	limit := new(big.Int).SetUint64(MaxComponent)
	for i := 0; i < c.n; i++ {
		if snap.p[i] == nil || snap.n[i] == nil ||
			snap.p[i].Sign() < 0 || snap.n[i].Sign() < 0 ||
			snap.p[i].Cmp(limit) >= 0 || snap.n[i].Cmp(limit) >= 0 {
			return ErrCorruptSnapshot
		}
		cp[i] = new(big.Int).Set(snap.p[i])
		cn[i] = new(big.Int).Set(snap.n[i])
	}
	r := c.replicas[id]
	r.mu.Lock()
	defer r.mu.Unlock()
	r.p = cp
	r.n = cn
	return nil
}

// SelfCheck verifies internal invariants of replica id.
func (c *Cluster) SelfCheck(id int) error {
	if !c.validID(id) {
		return ErrInvalidReplica
	}
	r := c.replicas[id]
	r.mu.Lock()
	defer r.mu.Unlock()
	limit := new(big.Int).SetUint64(MaxComponent)
	if len(r.p) != c.n || len(r.n) != c.n {
		return ErrCorruptState
	}
	for i := 0; i < c.n; i++ {
		if r.p[i] == nil || r.n[i] == nil ||
			r.p[i].Sign() < 0 || r.n[i].Sign() < 0 ||
			r.p[i].Cmp(limit) >= 0 || r.n[i].Cmp(limit) >= 0 {
			return ErrCorruptState
		}
	}
	return nil
}

// Size returns the number of replicas.
func (c *Cluster) Size() int { return c.n }

// ReplicaID returns the replica id recorded in the snapshot.
func (s *Snapshot) ReplicaID() int { return s.id }

// P returns a detached copy of the increment vector.
func (s *Snapshot) P() []*big.Int { return cloneVec(s.p) }

// N returns a detached copy of the decrement vector.
func (s *Snapshot) N() []*big.Int { return cloneVec(s.n) }

// Value returns sum(P) - sum(N) for the snapshot.
func (s *Snapshot) Value() *big.Int {
	v := new(big.Int)
	for i := range s.p {
		v.Add(v, s.p[i])
		v.Sub(v, s.n[i])
	}
	return v
}

func (c *Cluster) validID(id int) bool { return id >= 0 && id < c.n }

func (c *Cluster) update(id int, delta uint64, inc bool) error {
	if !c.validID(id) {
		return ErrInvalidReplica
	}
	if delta == 0 {
		return ErrNonPositiveDelta
	}
	r := c.replicas[id]
	r.mu.Lock()
	defer r.mu.Unlock()
	vec := r.n
	if inc {
		vec = r.p
	}
	sum := new(big.Int).Add(vec[id], new(big.Int).SetUint64(delta))
	if sum.Cmp(new(big.Int).SetUint64(MaxComponent)) >= 0 {
		return ErrComponentLimit
	}
	vec[id].Set(sum)
	return nil
}

// lockPair locks two distinct replicas in ascending id order so that
// reciprocal merges (a->b and b->a) running concurrently cannot deadlock.
func (c *Cluster) lockPair(x, y int) (*sync.Mutex, *sync.Mutex) {
	if x < y {
		c.replicas[x].mu.Lock()
		c.replicas[y].mu.Lock()
		return &c.replicas[x].mu, &c.replicas[y].mu
	}
	c.replicas[y].mu.Lock()
	c.replicas[x].mu.Lock()
	return &c.replicas[x].mu, &c.replicas[y].mu
}

func newSnapshot(id int, p, n []*big.Int) *Snapshot {
	return &Snapshot{id: id, p: cloneVec(p), n: cloneVec(n)}
}

func cloneVec(v []*big.Int) []*big.Int {
	out := make([]*big.Int, len(v))
	for i := range v {
		out[i] = new(big.Int).Set(v[i])
	}
	return out
}
