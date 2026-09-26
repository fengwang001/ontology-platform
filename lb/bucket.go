// Package lb holds the leaky bucket core: a FIFO queue of departure
// timestamps with the earliest departure at the head. It knows nothing
// about clock validation, configuration errors or concurrency; the sched
// package provides those on top.
package lb

import "fmt"

// Bucket is a fixed-capacity FIFO of request departure times.
// The zero value is not usable; use New.
type Bucket struct {
	q        []int64 // ring buffer, len == capacity
	head     int     // index of the earliest departure
	count    int     // queued (not yet drained) items
	capacity int64
	interval int64

	// drainChecks records how many queue entries were probed by the most
	// recent Drain call. Unexported on purpose: it is an internal
	// complexity probe, never part of the public API.
	drainChecks int
}

// New creates a bucket with the given capacity and interval.
// Callers must guarantee capacity >= 1 and interval >= 1.
func New(capacity, interval int64) *Bucket {
	return &Bucket{
		q:        make([]int64, int(capacity)),
		capacity: capacity,
		interval: interval,
	}
}

// Drain removes every entry whose departure time is <= t, starting from
// the head. It stops as soon as the head entry departs after t (or the
// queue is empty), so the number of probes is one past the number of
// removed entries at most.
func (b *Bucket) Drain(t int64) {
	b.drainChecks = 0
	for b.count > 0 {
		b.drainChecks++
		if b.q[b.head] > t {
			return // head has not departed; every later entry is even later
		}
		b.q[b.head] = 0
		b.head = (b.head + 1) % len(b.q)
		b.count--
	}
}

// Admit computes the departure time of a request arriving at t and appends
// it to the queue unless the bucket is full. It returns (departure, full);
// when full is true the request is rejected and no state changes.
func (b *Bucket) Admit(t int64) (dep int64, full bool) {
	if int64(b.count) == b.capacity {
		return 0, true
	}
	if b.count == 0 {
		dep = t + b.interval // empty bucket: first leak is one interval away
	} else {
		tail := (b.head + b.count - 1) % len(b.q)
		dep = b.q[tail] + b.interval // queue behind the previous item: smooth output
	}
	idx := (b.head + b.count) % len(b.q)
	b.q[idx] = dep
	b.count++
	return dep, false
}

// InSystem reports the number of items that have not leaked out yet.
func (b *Bucket) InSystem() int { return b.count }

// drainCheckCount exposes the internal probe counter to in-package tests
// only; there is intentionally no exported way to read it.
func (b *Bucket) drainCheckCount() int { return b.drainChecks }

// drainProbeBound is the accepted upper bound on probes for a Drain that
// finds the head still present.
const drainProbeBound = 2

// SelfCheck verifies internal complexity properties that external callers
// cannot observe directly: when nothing has departed, Drain probes the
// head alone and stops, so the probe count stays bounded regardless of the
// queue length. It returns only pass/fail — the probe value itself never
// crosses the package boundary.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		b := New(int64(m)+1, 1)
		for i := 0; i < m; i++ {
			if _, full := b.Admit(int64(i) * 1000); full {
				return fmt.Errorf("lb self-check: unexpected full at m=%d i=%d", m, i)
			}
		}
		b.Drain(0) // every departure is > 0: the head alone must be probed
		if b.drainChecks > drainProbeBound {
			// Pass/fail only: the probe count itself must never leave the
			// package through this exported entry point.
			return fmt.Errorf("lb self-check: drain probes scale with queue length at m=%d", m)
		}
	}
	return nil
}
