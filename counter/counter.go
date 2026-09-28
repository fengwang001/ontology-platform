// Package counter implements a conflict-free replicated PN-counter
// (positive-negative counter). Each replica keeps two grow-only vectors:
// increments P and decrements N. A local inc/dec modifies only that
// replica's own component. Merge takes the component-wise maximum of both
// sides and only mutates the destination replica. The value of any replica
// is sum(P)-sum(N) and may be negative.
package counter

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
)

// Sentinel errors identify the distinct rejection reasons. Use errors.Is to
// classify an *OpError returned by any operation.
var (
	// ErrInvalidArgument: constructor/configuration arguments are illegal.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrUnknownReplica: a replica id is out of range.
	ErrUnknownReplica = errors.New("unknown replica")
	// ErrNonPositive: an inc/dec delta is zero.
	ErrNonPositive = errors.New("delta must be positive")
	// ErrOverflow: applying the change would push a component above Limit.
	ErrOverflow = errors.New("component would exceed limit")
	// ErrSelfInconsistent: SelfCheck found a violated invariant.
	ErrSelfInconsistent = errors.New("counter state is self-inconsistent")
)

// OpError wraps a rejected operation with its name, a human-readable reason
// and one of the sentinel errors. Every rejected operation returns an
// *OpError and leaves all state untouched.
type OpError struct {
	Op     string // "new", "inc", "dec", "merge", "value", "selfcheck"
	Reason string // distinguishable, human-readable detail
	Err    error  // one of the sentinel errors
}

func (e *OpError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("counter %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("counter %s: %s: %v", e.Op, e.Reason, e.Err)
}

func (e *OpError) Unwrap() error { return e.Err }

func opError(op string, err error, format string, args ...any) error {
	return &OpError{Op: op, Reason: fmt.Sprintf(format, args...), Err: err}
}

// replica is the per-replica state; every field is guarded by mu.
type replica struct {
	mu sync.Mutex
	p  []uint64 // observed increment vector
	n  []uint64 // observed decrement vector
}

// Counter is a set of replicas. It is safe for concurrent use: each replica
// has its own lock; multi-replica operations acquire locks in ascending
// replica-id order, so mutually inverse merges (Merge(a,b) concurrently with
// Merge(b,a)) can never deadlock.
type Counter struct {
	replicas int
	limit    uint64
	rs       []*replica
}

// New creates a counter with replicas replicas (ids 0..replicas-1), all
// vectors starting at zero. limit is the inclusive upper bound of every
// vector component (use math.MaxUint64 for "no practical limit"); an
// inc/dec/merge that would make any component exceed it is rejected.
func New(replicas int, limit uint64) (*Counter, error) {
	const op = "new"
	if replicas <= 0 {
		return nil, opError(op, ErrInvalidArgument,
			"replicas must be positive, got %d", replicas)
	}
	if limit == 0 {
		return nil, opError(op, ErrInvalidArgument, "limit must be greater than zero")
	}
	rs := make([]*replica, replicas)
	for i := range rs {
		rs[i] = &replica{
			p: make([]uint64, replicas),
			n: make([]uint64, replicas),
		}
	}
	return &Counter{replicas: replicas, limit: limit, rs: rs}, nil
}

func (c *Counter) checkID(op string, id int) error {
	if id < 0 || id >= c.replicas {
		return opError(op, ErrUnknownReplica,
			"replica id %d out of range [0,%d)", id, c.replicas)
	}
	return nil
}

// addLocked validates before touching state: rejection leaves no trace.
// The caller holds r.mu.
func (c *Counter) addLocked(op string, r *replica, id, component int, delta uint64, vec []uint64) error {
	if delta == 0 {
		return opError(op, ErrNonPositive, "delta must be > 0, got 0")
	}
	cur := vec[component]
	if cur > c.limit || delta > c.limit-cur {
		return opError(op, ErrOverflow,
			"replica %d component %d would become %d, above limit %d",
			id, component, cur+delta, c.limit)
	}
	vec[component] = cur + delta
	return nil
}

// Inc adds delta to the replica's own increment component.
func (c *Counter) Inc(replicaID int, delta uint64) error {
	return c.apply("inc", replicaID, delta, true)
}

// Dec records delta decrements on the replica's own component. Values are
// never truncated: the resulting counter value may be negative.
func (c *Counter) Dec(replicaID int, delta uint64) error {
	return c.apply("dec", replicaID, delta, false)
}

func (c *Counter) apply(op string, replicaID int, delta uint64, isInc bool) error {
	if err := c.checkID(op, replicaID); err != nil {
		return err
	}
	r := c.rs[replicaID]
	r.mu.Lock()
	defer r.mu.Unlock()
	if isInc {
		return c.addLocked(op, r, replicaID, replicaID, delta, r.p)
	}
	return c.addLocked(op, r, replicaID, replicaID, delta, r.n)
}

// Merge merges src into dst: for every component, each of dst's vectors
// becomes max(dst, src). Only dst is mutated. dst == src is a legal no-op.
// The merge is rejected atomically (dst untouched) if a component maximum
// would exceed Limit; components never shrink under max, so overflow can
// only arise from state produced against a larger limit, which is never
// silently truncated.
func (c *Counter) Merge(dst, src int) error {
	const op = "merge"
	if err := c.checkID(op, dst); err != nil {
		return err
	}
	if err := c.checkID(op, src); err != nil {
		return err
	}
	if dst == src {
		return nil // legal idempotent no-op
	}

	// Stable ascending lock order prevents deadlock between Merge(a,b)
	// running concurrently with Merge(b,a).
	lo, hi := dst, src
	if lo > hi {
		lo, hi = hi, lo
	}
	c.rs[lo].mu.Lock()
	defer c.rs[lo].mu.Unlock()
	c.rs[hi].mu.Lock()
	defer c.rs[hi].mu.Unlock()

	d, s := c.rs[dst], c.rs[src]
	if err := c.validateMergeLocked(op, dst, d, s); err != nil {
		return err
	}
	for i := 0; i < c.replicas; i++ {
		if s.p[i] > d.p[i] {
			d.p[i] = s.p[i]
		}
		if s.n[i] > d.n[i] {
			d.n[i] = s.n[i]
		}
	}
	return nil
}

func (c *Counter) validateMergeLocked(op string, dst int, d, s *replica) error {
	for i := 0; i < c.replicas; i++ {
		m := s.p[i]
		if d.p[i] > m {
			m = d.p[i]
		}
		if m > c.limit {
			return opError(op, ErrOverflow,
				"replica %d increment component %d would become %d, above limit %d",
				dst, i, m, c.limit)
		}
		m = s.n[i]
		if d.n[i] > m {
			m = d.n[i]
		}
		if m > c.limit {
			return opError(op, ErrOverflow,
				"replica %d decrement component %d would become %d, above limit %d",
				dst, i, m, c.limit)
		}
	}
	return nil
}

// Value returns sum(P)-sum(N) for the replica. It is computed with
// arbitrary-precision integers so it is exact even beyond int64, and it may
// be negative.
func (c *Counter) Value(replicaID int) (*big.Int, error) {
	p, n, err := c.ReplicaSnapshot(replicaID)
	if err != nil {
		return nil, err
	}
	return valueOf(p, n), nil
}

func valueOf(p, n []uint64) *big.Int {
	v := new(big.Int)
	buf := new(big.Int)
	for _, x := range p {
		v.Add(v, buf.SetUint64(x))
	}
	for _, x := range n {
		v.Sub(v, buf.SetUint64(x))
	}
	return v
}

// ReplicaSnapshot returns defensive copies of the replica's P and N vectors.
func (c *Counter) ReplicaSnapshot(replicaID int) (p, n []uint64, err error) {
	const op = "value"
	if err := c.checkID(op, replicaID); err != nil {
		return nil, nil, err
	}
	r := c.rs[replicaID]
	r.mu.Lock()
	defer r.mu.Unlock()
	p = append([]uint64(nil), r.p...)
	n = append([]uint64(nil), r.n...)
	return p, n, nil
}

// Snapshot is a point-in-time, defensive-copy snapshot of every replica.
// P[i]/N[i] are replica i's vectors.
type Snapshot struct {
	Replicas int
	Limit    uint64
	P        [][]uint64
	N        [][]uint64
}

// Snapshot returns the state of all replicas under the global ascending
// lock order, giving a consistent point-in-time view.
func (c *Counter) Snapshot() Snapshot {
	for i := 0; i < c.replicas; i++ {
		c.rs[i].mu.Lock()
	}
	defer func() {
		for i := c.replicas - 1; i >= 0; i-- {
			c.rs[i].mu.Unlock()
		}
	}()

	ps := make([][]uint64, c.replicas)
	ns := make([][]uint64, c.replicas)
	for i := 0; i < c.replicas; i++ {
		ps[i] = append([]uint64(nil), c.rs[i].p...)
		ns[i] = append([]uint64(nil), c.rs[i].n...)
	}
	return Snapshot{Replicas: c.replicas, Limit: c.limit, P: ps, N: ns}
}

// Values returns the current value of every replica from one snapshot.
func (c *Counter) Values() []*big.Int {
	snap := c.Snapshot()
	out := make([]*big.Int, c.replicas)
	for i := 0; i < c.replicas; i++ {
		out[i] = valueOf(snap.P[i], snap.N[i])
	}
	return out
}

// SelfCheck verifies the internal invariants: correct vector sizes and every
// component within [0, Limit]. It returns an *OpError wrapping
// ErrSelfInconsistent describing every violation, or nil when sound.
func (c *Counter) SelfCheck() error {
	for i := 0; i < c.replicas; i++ {
		c.rs[i].mu.Lock()
	}
	defer func() {
		for i := c.replicas - 1; i >= 0; i-- {
			c.rs[i].mu.Unlock()
		}
	}()

	var problems []string
	for id := 0; id < c.replicas; id++ {
		r := c.rs[id]
		if len(r.p) != c.replicas || len(r.n) != c.replicas {
			problems = append(problems, fmt.Sprintf(
				"replica %d vector length p=%d n=%d, want %d",
				id, len(r.p), len(r.n), c.replicas))
			continue
		}
		for k, v := range r.p {
			if v > c.limit {
				problems = append(problems, fmt.Sprintf(
					"replica %d p[%d]=%d exceeds limit %d", id, k, v, c.limit))
			}
		}
		for k, v := range r.n {
			if v > c.limit {
				problems = append(problems, fmt.Sprintf(
					"replica %d n[%d]=%d exceeds limit %d", id, k, v, c.limit))
			}
		}
	}
	if len(problems) > 0 {
		return opError("selfcheck", ErrSelfInconsistent,
			"%d violation(s): %s", len(problems), strings.Join(problems, "; "))
	}
	return nil
}

// Replicas reports the number of replicas.
func (c *Counter) Replicas() int { return c.replicas }

// Limit reports the configured per-component upper bound.
func (c *Counter) Limit() uint64 { return c.limit }
