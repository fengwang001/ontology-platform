// Package replay implements a deterministic rate-limited event replay player
// built on an integer token bucket driven by a monotonic logical clock.
package replay

import (
	"errors"
	"fmt"
	"sync"
)

// Distinguishable failure causes.
var (
	ErrClockBackwards = errors.New("replay: logical clock moved backwards")
	ErrInvalidConfig  = errors.New("replay: invalid configuration")
	ErrQueueFull      = errors.New("replay: enqueue rejected: queue capacity exceeded")
)

// Config configures a Replayer.
// All rates are integer tokens per logical time unit.
type Config struct {
	BaseRate    int64 // refill rate while the queue is empty
	CatchupRate int64 // refill rate while the queue has a backlog
	BucketCap   int64 // maximum number of tokens the bucket can hold
	QueueLimit  int64 // maximum number of events the queue may hold
}

// Replayer smooths queued events using an integer token bucket.
//
// All state transitions are serialized behind a single mutex; read-only
// queries take the read lock, so they may run concurrently with each other
// and always observe a self-consistent state.
type Replayer struct {
	mu sync.RWMutex

	cfg Config

	clock  int64 // current logical time (monotonic, never decreases)
	tokens int64 // tokens currently available, always in [0, BucketCap]

	// FIFO event queue. head is the index of the next event to drain;
	// pending entries occupy q[head:len(q)]. The slice is compacted when
	// head gets large so enqueue stays on an append-only backing store.
	q    []int64
	head int

	enqueuedTotal int64
	drainedTotal  int64
}

// Snapshot is a point-in-time, internally consistent view of a Replayer.
type Snapshot struct {
	Clock         int64
	Tokens        int64
	Pending       int64
	EnqueuedTotal int64
	DrainedTotal  int64
}

// New creates a Replayer. It returns an error wrapping ErrInvalidConfig when
// any parameter is illegal.
func New(cfg Config) (*Replayer, error) {
	switch {
	case cfg.BaseRate <= 0:
		return nil, fmt.Errorf("%w: base rate must be positive, got %d", ErrInvalidConfig, cfg.BaseRate)
	case cfg.CatchupRate <= 0:
		return nil, fmt.Errorf("%w: catch-up rate must be positive, got %d", ErrInvalidConfig, cfg.CatchupRate)
	case cfg.CatchupRate < cfg.BaseRate:
		return nil, fmt.Errorf("%w: catch-up rate %d must be >= base rate %d", ErrInvalidConfig, cfg.CatchupRate, cfg.BaseRate)
	case cfg.BucketCap <= 0:
		return nil, fmt.Errorf("%w: bucket capacity must be positive, got %d", ErrInvalidConfig, cfg.BucketCap)
	case cfg.QueueLimit <= 0:
		return nil, fmt.Errorf("%w: queue limit must be positive, got %d", ErrInvalidConfig, cfg.QueueLimit)
	}
	return &Replayer{cfg: cfg}, nil
}

// Advance moves the logical clock to now (no-op when now equals the current
// clock) and refills the bucket according to the queue state at refill time:
// CatchupRate while the queue has a backlog, BaseRate when it is empty.
// Refilled tokens are capped at BucketCap. It returns an error wrapping
// ErrClockBackwards without changing any state if now is in the past.
func (r *Replayer) Advance(now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.clock {
		return fmt.Errorf("%w: now=%d < clock=%d", ErrClockBackwards, now, r.clock)
	}
	r.refillLocked(now)
	return nil
}

// Enqueue appends events to the tail of the FIFO queue after advancing the
// clock to now. The whole batch is rejected (wrapping ErrQueueFull) if it
// would exceed QueueLimit; on any failure the clock, bucket and queue are
// left untouched. Events are appended in slice order.
func (r *Replayer) Enqueue(now int64, events []int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.clock {
		return fmt.Errorf("%w: now=%d < clock=%d", ErrClockBackwards, now, r.clock)
	}
	pending := int64(len(r.q) - r.head)
	n := int64(len(events))
	if n > r.cfg.QueueLimit-pending {
		return fmt.Errorf("%w: pending=%d + batch=%d > limit=%d", ErrQueueFull, pending, n, r.cfg.QueueLimit)
	}
	// Validation passed: only now may state change.
	r.refillLocked(now)
	r.q = append(r.q, events...)
	r.enqueuedTotal += n
	return nil
}

// Drain advances the clock to now, refills the bucket, and then emits queued
// events in enqueue order, consuming one token per event, until tokens run
// out or the queue is empty. The returned slice is owned by the caller.
func (r *Replayer) Drain(now int64) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.clock {
		return nil, fmt.Errorf("%w: now=%d < clock=%d", ErrClockBackwards, now, r.clock)
	}
	r.refillLocked(now)

	pending := int64(len(r.q) - r.head)
	take := r.tokens
	if take > pending {
		take = pending
	}
	if take <= 0 {
		return nil, nil
	}
	emitted := append([]int64(nil), r.q[r.head:r.head+int(take)]...)
	r.head += int(take)
	r.tokens -= take
	r.drainedTotal += take

	// Compact when everything queued so far has been drained, or when the
	// discarded prefix dominates the backing array.
	if r.head == len(r.q) || r.head > cap(r.q)/2 {
		r.q = append(r.q[:0], r.q[r.head:]...)
		r.head = 0
	}
	return emitted, nil
}

// Clock returns the current logical clock value.
func (r *Replayer) Clock() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clock
}

// Tokens returns the number of available tokens.
func (r *Replayer) Tokens() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tokens
}

// Len returns the number of events currently queued.
func (r *Replayer) Len() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int64(len(r.q) - r.head)
}

// Totals returns cumulative enqueue and drain counters.
func (r *Replayer) Totals() (enqueued, drained int64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enqueuedTotal, r.drainedTotal
}

// Snapshot returns a point-in-time, internally consistent view. All fields
// come from the same read-lock critical section, so concurrent readers always
// see matching clock, tokens, pending length and totals.
func (r *Replayer) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Snapshot{
		Clock:         r.clock,
		Tokens:        r.tokens,
		Pending:       int64(len(r.q) - r.head),
		EnqueuedTotal: r.enqueuedTotal,
		DrainedTotal:  r.drainedTotal,
	}
}

// SelfCheck verifies the internal invariants:
//   - 0 <= tokens <= BucketCap,
//   - 0 <= pending <= QueueLimit,
//   - EnqueuedTotal == DrainedTotal + pending.
func (r *Replayer) SelfCheck() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	pending := int64(len(r.q) - r.head)
	switch {
	case r.tokens < 0 || r.tokens > r.cfg.BucketCap:
		return fmt.Errorf("replay: tokens %d out of range [0,%d]", r.tokens, r.cfg.BucketCap)
	case pending < 0 || pending > r.cfg.QueueLimit:
		return fmt.Errorf("replay: pending %d out of range [0,%d]", pending, r.cfg.QueueLimit)
	case r.enqueuedTotal < 0 || r.drainedTotal < 0:
		return fmt.Errorf("replay: negative totals: enqueued=%d drained=%d", r.enqueuedTotal, r.drainedTotal)
	case r.enqueuedTotal != r.drainedTotal+pending:
		return fmt.Errorf("replay: totals invariant broken: enqueued=%d, drained=%d, pending=%d",
			r.enqueuedTotal, r.drainedTotal, pending)
	}
	return nil
}

// refillLocked advances the clock to now and adds integer tokens for the
// elapsed time, choosing the rate from the queue state before the refill
// (backlog => CatchupRate, empty => BaseRate), and caps at BucketCap.
// Caller must hold r.mu and have verified now >= r.clock.
func (r *Replayer) refillLocked(now int64) {
	elapsed := now - r.clock
	if elapsed == 0 {
		r.clock = now
		return
	}
	rate := r.cfg.BaseRate
	if len(r.q)-r.head > 0 {
		rate = r.cfg.CatchupRate
	}
	// Avoid overflow of elapsed*rate: if the raw product would already push
	// tokens past the capacity, saturation at the cap is exact.
	room := r.cfg.BucketCap - r.tokens
	if elapsed > room/rate {
		r.tokens = r.cfg.BucketCap
	} else {
		r.tokens += elapsed * rate
	}
	r.clock = now
}
