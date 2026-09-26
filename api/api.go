// Package api is the outward face of the least-connections load
// balancer. It validates configuration, serializes access with a mutex
// (safe for concurrent use), and exposes SelfCheck.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/svc"
)

// ErrBadConfig rejects a non-positive server count in New.
var ErrBadConfig = errors.New("api: server count must be >= 1")

// Re-exported so callers can distinguish all three failure kinds.
var (
	ErrBadIndex  = svc.ErrBadIndex
	ErrUnderflow = svc.ErrUnderflow
)

// LB is a concurrency-safe least-connections load balancer.
type LB struct {
	mu  sync.Mutex
	mgr *svc.Manager
}

// New builds a balancer for n servers. Requires n >= 1.
func New(n int) (*LB, error) {
	if n < 1 {
		return nil, ErrBadConfig
	}
	return &LB{mgr: svc.NewManager(n)}, nil
}

// Pick returns the least-connected server's index (smallest on ties).
func (l *LB) Pick() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mgr.Pick()
}

// Acquire adds one connection to server i.
func (l *LB) Acquire(i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mgr.Acquire(i)
}

// Release removes one connection from server i, refusing underflow.
func (l *LB) Release(i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mgr.Release(i)
}

// Count reports server i's active connections.
func (l *LB) Count(i int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.mgr.Count(i)
}

// naivePick is the O(n) reference: fewest connections, smallest index.
func naivePick(counts []int) int {
	best := 0
	for i := 1; i < len(counts); i++ {
		if counts[i] < counts[best] {
			best = i
		}
	}
	return best
}

// SelfCheck runs built-in operation sequences against fresh instances
// and verifies the four invariants: naive-reference agreement,
// non-negative counts, conservation, and no-trace-on-reject. It never
// touches the receiver's state, so it is safe to call concurrently.
func (l *LB) SelfCheck() error {
	for _, n := range []int{1, 2, 3, 7, 64} {
		if err := selfCheckN(n); err != nil {
			return err
		}
	}
	return nil
}

// selfCheckN drives a deterministic pseudo-random op sequence over n
// servers, checking every invariant after every step.
func selfCheckN(n int) error {
	lb, err := New(n)
	if err != nil {
		return err
	}
	ref := make([]int, n) // naive reference counts
	acquired, released := 0, 0
	seed := uint64(n)*2654435761 + 1
	rand := func(m int) int { // xorshift, deterministic
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(m))
	}
	for step := 0; step < 400; step++ {
		i := rand(n + 2) // sometimes out of range -> must be rejected
		switch rand(4) {
		case 0, 1: // acquire
			if err := lb.Acquire(i); err != nil {
				if !errors.Is(err, ErrBadIndex) {
					return fmt.Errorf("selfcheck n=%d: acquire: %w", n, err)
				}
			} else {
				ref[i]++
				acquired++
			}
		case 2: // release
			if err := lb.Release(i); err != nil {
				if !errors.Is(err, ErrBadIndex) && !errors.Is(err, ErrUnderflow) {
					return fmt.Errorf("selfcheck n=%d: release: %w", n, err)
				}
			} else {
				ref[i]--
				released++
			}
		default: // pick, then verify all invariants
			if got := lb.Pick(); got != naivePick(ref) {
				return fmt.Errorf("selfcheck n=%d: pick=%d, naive=%d", n, got, naivePick(ref))
			}
		}
		sum := 0
		for j := 0; j < n; j++ {
			c, err := lb.Count(j)
			if err != nil || c != ref[j] || c < 0 {
				return fmt.Errorf("selfcheck n=%d: count(%d)=%d, ref=%d, err=%v", n, j, c, ref[j], err)
			}
			sum += c
		}
		if sum != acquired-released {
			return fmt.Errorf("selfcheck n=%d: sum=%d, acquired-released=%d", n, sum, acquired-released)
		}
	}
	return nil
}
