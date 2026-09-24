// Package fanout implements a single-subscriber bounded FIFO queue with
// tail-drop semantics. It depends on no other package.
package fanout

import (
	"errors"
	"sync"
)

// ErrInvalidCapacity is returned when a requested capacity is <= 0.
var ErrInvalidCapacity = errors.New("fanout: capacity must be >= 1")

// errSlotCountGrows is returned by VerifyFullnessCost when the number of
// inspected slots depends on the capacity. It deliberately carries no
// counter value.
var errSlotCountGrows = errors.New("fanout: fullness check inspects a number of slots that grows with capacity")

// slotBound is the small constant the per-Enqueue slot inspection must
// never exceed regardless of capacity.
const slotBound = 1

// Queue is a capacity-C FIFO of int64 events for one subscriber.
// When full, a new event is tail-dropped: the newest event is rejected,
// the queued events stay untouched, and the drop counter increments.
type Queue struct {
	mu sync.Mutex

	buf  []int64 // fixed-size ring buffer
	cap  int     // fixed capacity C
	head int     // index of the oldest event
	n    int     // current number of queued events (maintained field)

	drop int // cumulative tail-dropped events

	// slotsChecked records how many buffer slots the most recent Enqueue
	// inspected while deciding "is this queue full?". Fullness is decided
	// by comparing the maintained field n against cap, so zero slots are
	// inspected and the value never grows with cap. Unexported on purpose:
	// callers must never read the number through the public API.
	slotsChecked int
}

// New creates an empty queue of capacity c.
func New(c int) (*Queue, error) {
	if c <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Queue{buf: make([]int64, c), cap: c}, nil
}

// Enqueue appends ev to the tail, or tail-drops ev (and bumps the drop
// counter) when the queue is full. It never blocks.
func (q *Queue) Enqueue(ev int64) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.slotsChecked = 0 // decide fullness from the maintained n field: no slot scan
	if q.n == q.cap {
		q.drop++ // tail-drop: keep old events, reject the newcomer
		return
	}
	q.buf[(q.head+q.n)%q.cap] = ev
	q.n++
}

// Dequeue removes and returns the head (oldest) event; ok is false when
// the queue is empty.
func (q *Queue) Dequeue() (ev int64, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.n == 0 {
		return 0, false
	}
	ev = q.buf[q.head]
	q.head = (q.head + 1) % q.cap
	q.n--
	return ev, true
}

// DropCount returns the cumulative number of tail-dropped events.
func (q *Queue) DropCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.drop
}

// Len returns the current queue length.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.n
}

// VerifyFullnessCost is a boolean self-verifier: it builds queues at
// several capacities and fails if the per-Enqueue slot inspection grows
// with capacity. It returns only a nil/non-nil verdict; the counter
// value itself never crosses the package boundary.
func VerifyFullnessCost() error {
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		q, err := New(m)
		if err != nil {
			return err
		}
		q.Enqueue(1) // empty queue
		if q.slotsChecked > slotBound {
			return errSlotCountGrows
		}
		for i := 1; i < m; i++ {
			q.Enqueue(int64(i + 1))
		}
		before := q.slotsChecked
		q.Enqueue(999) // full queue: tail-drop path
		if q.slotsChecked > slotBound || q.slotsChecked != before {
			return errSlotCountGrows
		}
	}
	return nil
}
