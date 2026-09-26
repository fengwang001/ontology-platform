// Package gc implements a grow-only counter (G-counter) CRDT:
// a sparse map from node id to a non-negative, monotonically
// growing count. It has no dependencies on other packages.
package gc

import (
	"errors"
	"sync/atomic"
)

// Sentinel errors, distinguishable via errors.Is.
var (
	ErrNonPositiveInc = errors.New("gc: increment must be positive")
	ErrNegativeNode   = errors.New("gc: node id must be non-negative")
	ErrNegativeEntry  = errors.New("gc: entry count must be non-negative")
)

// lastMergeReads records how many entries the most recent Merge
// read. Non-exported on purpose: it is observable only from tests
// living inside this package, never through the public API.
var lastMergeReads atomic.Int64

// Counter is a G-counter: node id -> count, counts only grow.
type Counter struct {
	entries map[int]int
}

// New returns an empty Counter.
func New() *Counter { return &Counter{entries: make(map[int]int)} }

// FromMap validates a raw map and returns a Counter owning a copy.
// Negative node ids or counts are rejected without side effects.
func FromMap(m map[int]int) (*Counter, error) {
	for node, count := range m {
		if node < 0 {
			return nil, ErrNegativeNode
		}
		if count < 0 {
			return nil, ErrNegativeEntry
		}
	}
	c := New()
	for node, count := range m {
		c.entries[node] = count
	}
	return c, nil
}

// Inc adds k to this node's own entry. k must be > 0 and node >= 0;
// rejected calls validate first and leave the counter untouched.
func (c *Counter) Inc(node, k int) error {
	if k <= 0 {
		return ErrNonPositiveInc
	}
	if node < 0 {
		return ErrNegativeNode
	}
	c.entries[node] += k
	return nil
}

// valid reports whether every entry is legal (non-negative).
func (c *Counter) valid() bool {
	for node, count := range c.entries {
		if node < 0 || count < 0 {
			return false
		}
	}
	return true
}

// Merge returns a new Counter holding the per-entry maximum of a
// and b (missing entries count as 0). It is commutative and
// idempotent. Either input containing a negative entry fails the
// whole merge; inputs are never mutated.
func Merge(a, b *Counter) (*Counter, error) {
	if !a.valid() || !b.valid() {
		return nil, ErrNegativeEntry
	}
	lastMergeReads.Store(int64(len(a.entries) + len(b.entries)))
	out := New()
	for node, count := range a.entries {
		out.entries[node] = count
	}
	for node, count := range b.entries {
		if count > out.entries[node] {
			out.entries[node] = count
		}
	}
	return out, nil
}

// Value sums all entries of c.
func Value(c *Counter) int {
	sum := 0
	for _, count := range c.entries {
		sum += count
	}
	return sum
}

// Snapshot returns a copy of the entries (for display/testing).
func (c *Counter) Snapshot() map[int]int {
	out := make(map[int]int, len(c.entries))
	for node, count := range c.entries {
		out[node] = count
	}
	return out
}
