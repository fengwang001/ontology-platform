// Package boundedqueue provides a bounded queue with head-only lazy expiry
// and two overflow policies (drop-head and reject).
package boundedqueue

import (
	"errors"
	"sync"
)

// Reason explains why a message was moved to the dead-letter log.
type Reason string

const (
	ReasonExpired  Reason = "expired"
	ReasonOverflow Reason = "overflow"
)

// OverflowPolicy decides what happens when an enqueue finds the queue full
// after head-only expiry cleanup.
type OverflowPolicy int

const (
	PolicyDropHead OverflowPolicy = iota
	PolicyReject
)

// RejectError is returned when an operation is rejected.
type RejectError struct {
	Kind string
}

func (e *RejectError) Error() string {
	return "boundedqueue: rejected: " + e.Kind
}

// Sentinel errors for errors.Is checks.
var (
	ErrInvalidCapacity = errors.New("boundedqueue: capacity must be >= 1")
	ErrNegativeTTL     = errors.New("boundedqueue: ttl must be >= 0")
	ErrClockBackward   = &RejectError{Kind: "clock_backward"}
	ErrQueueFull       = &RejectError{Kind: "queue_full"}
	ErrEmptyQueue      = &RejectError{Kind: "empty_queue"}
)

// DeadLetter is a message removed from the queue without a successful dequeue.
type DeadLetter[T any] struct {
	Message  T
	Deadline int64
	Reason   Reason
}

type entry[T any] struct {
	msg      T
	deadline int64
}

// Queue is a concurrency-safe bounded queue with lazy head expiry.
type Queue[T any] struct {
	mu       sync.Mutex
	capacity int
	policy   OverflowPolicy
	buf      []entry[T] // active entries are buf[head:]; head>0 is compacted away
	head     int
	dead     []DeadLetter[T]
	maxNow   int64
	hasClock bool
}

// New creates a Queue with the given capacity and overflow policy.
func New[T any](capacity int, policy OverflowPolicy) (*Queue[T], error) {
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Queue[T]{
		capacity: capacity,
		policy:   policy,
		buf:      make([]entry[T], 0, capacity),
	}, nil
}

// Enqueue appends a message with (now+ttl) as its deadline.
func (q *Queue[T]) Enqueue(msg T, ttl, now int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Clock regression takes priority over every other rejection reason.
	if q.hasClock && now < q.maxNow {
		return ErrClockBackward
	}
	if ttl < 0 {
		return ErrNegativeTTL
	}

	// Expiry cleanup runs only while the queue is full, and only at the head.
	if len(q.buf)-q.head == q.capacity {
		n := q.countHeadExpired(now)
		if n > 0 && q.policy == PolicyReject {
			// Simulate the cleanup first: if the queue is still full
			// afterwards the whole op is rejected and nothing may land,
			// including the simulated expiry removals.
			if len(q.buf)-q.head-n == q.capacity {
				return ErrQueueFull
			}
		}
		if n > 0 {
			q.commitHeadExpiry(now)
		}
		if q.policy == PolicyReject && len(q.buf)-q.head == q.capacity {
			return ErrQueueFull
		}
		if q.policy == PolicyDropHead && len(q.buf)-q.head == q.capacity {
			headEntry := q.buf[q.head]
			q.head++
			q.compact()
			q.dead = append(q.dead, DeadLetter[T]{
				Message:  headEntry.msg,
				Deadline: headEntry.deadline,
				Reason:   ReasonOverflow,
			})
		}
	}

	q.buf = append(q.buf, entry[T]{msg: msg, deadline: now + ttl})
	q.maxNow = now
	q.hasClock = true
	return nil
}

// Dequeue removes and returns the head after head-only expiry cleanup.
func (q *Queue[T]) Dequeue(now int64) (T, error) {
	var zero T
	q.mu.Lock()
	defer q.mu.Unlock()

	// Clock regression takes priority over every other rejection reason.
	if q.hasClock && now < q.maxNow {
		return zero, ErrClockBackward
	}

	n := q.countHeadExpired(now)
	if len(q.buf)-q.head-n == 0 {
		// Empty after cleanup: the cleanup itself must not land either.
		return zero, ErrEmptyQueue
	}
	if n > 0 {
		q.commitHeadExpiry(now)
	}

	headEntry := q.buf[q.head]
	q.head++
	q.compact()
	q.maxNow = now
	q.hasClock = true
	return headEntry.msg, nil
}

// Len reports the number of messages still held in the queue.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.buf) - q.head
}

// DeadLetters returns a snapshot copy of the dead-letter log in arrival order.
func (q *Queue[T]) DeadLetters() []DeadLetter[T] {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.dead) == 0 {
		return nil
	}
	out := make([]DeadLetter[T], len(q.dead))
	copy(out, q.dead)
	return out
}

// countHeadExpired counts consecutive expired entries starting at the head.
// A message is expired when now >= deadline.
func (q *Queue[T]) countHeadExpired(now int64) int {
	n := 0
	for i := q.head; i < len(q.buf); i++ {
		if now < q.buf[i].deadline {
			break
		}
		n++
	}
	return n
}

// commitHeadExpiry moves consecutive expired head entries to the dead-letter
// log as ReasonExpired, in queue order.
func (q *Queue[T]) commitHeadExpiry(now int64) {
	for q.head < len(q.buf) && now >= q.buf[q.head].deadline {
		headEntry := q.buf[q.head]
		q.head++
		q.dead = append(q.dead, DeadLetter[T]{
			Message:  headEntry.msg,
			Deadline: headEntry.deadline,
			Reason:   ReasonExpired,
		})
	}
	q.compact()
}

// compact reclaims head space so append can never exceed the capacity bound.
func (q *Queue[T]) compact() {
	if q.head == 0 {
		return
	}
	if q.head == len(q.buf) {
		q.buf = q.buf[:0]
	} else {
		copy(q.buf, q.buf[q.head:])
		q.buf = q.buf[:len(q.buf)-q.head]
	}
	q.head = 0
}
