// Package bq is the bounded FIFO queue core. It depends on no other package
// in this module. Concurrency safety is provided internally.
package bq

import "sync"

// Queue is a bounded FIFO queue backed by a ring buffer. Element identity is
// not tracked: slots only mark occupancy. A head pointer advances on consume,
// so draining never shifts or copies the surviving elements.
type Queue struct {
	mu sync.Mutex

	capacity int64
	buf      []struct{} // ring slots; length == capacity
	head     int64      // index of the oldest occupied slot
	count    int64      // occupied slots, always 0 <= count <= capacity

	// lastConsumeMoved counts elements moved/copied during the most recent
	// Consume. Unexported on purpose: it must never appear in the public API.
	// With a head pointer the answer is always 0.
	lastConsumeMoved int64
}

// New creates a queue with the given capacity. The caller (bp/api) is
// responsible for rejecting capacity < 1.
func New(capacity int64) *Queue {
	return &Queue{
		capacity: capacity,
		buf:      make([]struct{}, capacity),
	}
}

// Produce attempts to place n elements as one all-or-nothing batch.
// n >= 1 is a precondition validated by the caller. It returns false when the
// queue is full (backpressure); in that case no slot is touched.
func (q *Queue) Produce(n int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.count+n > q.capacity {
		return false
	}
	for i := int64(0); i < n; i++ {
		q.buf[(q.head+q.count+i)%q.capacity] = struct{}{}
	}
	q.count += n
	return true
}

// Consume removes n elements as one all-or-nothing batch. n >= 1 and
// n <= count are preconditions validated by the caller; it returns false on
// underflow without changing anything. Advancing the head pointer moves zero
// elements, which is recorded in lastConsumeMoved.
func (q *Queue) Consume(n int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	if n > q.count {
		q.lastConsumeMoved = 0
		return false
	}
	for i := int64(0); i < n; i++ {
		q.buf[(q.head+i)%q.capacity] = struct{}{}
	}
	q.head = (q.head + n) % q.capacity
	q.count -= n
	q.lastConsumeMoved = 0 // head pointer only; no surviving element is moved
	return true
}

// Count returns the number of elements currently held.
func (q *Queue) Count() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.count
}

// Free returns the number of free slots.
func (q *Queue) Free() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.capacity - q.count
}
