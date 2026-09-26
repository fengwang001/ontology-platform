// Package bq is the bounded FIFO queue core. It depends on no other package.
// It stores elements in a ring buffer and tracks the head with a pointer so
// that Consume never shifts/copies the backing slice.
package bq

import "sync"

// Ring is a fixed-capacity FIFO ring. The zero value is not usable; use New.
type Ring struct {
	mu       sync.Mutex
	capacity int64
	buf      []int // ring slots; valid for count elements starting at head
	head     int64 // index of the oldest element
	count    int64 // current number of elements: 0 <= count <= capacity
	nextTok  int   // monotonic token written into newly produced slots

	// moved counts elements moved/copied during the most recent Consume.
	// Unexported on purpose: it must never appear in the public API.
	moved int64
}

// New creates a Ring with the given capacity. The caller (bp) is responsible
// for rejecting capacity <= 0.
func New(capacity int64) *Ring {
	return &Ring{
		capacity: capacity,
		buf:      make([]int, int(capacity)),
	}
}

// TryProduce places all n elements or none. It returns false (and changes no
// state) when count+n would exceed capacity. n >= 1 is validated by bp.
func (r *Ring) TryProduce(n int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count+n > r.capacity {
		return false // backpressure: nothing is placed
	}
	for i := int64(0); i < n; i++ {
		idx := (r.head + r.count) % r.capacity
		r.nextTok++
		r.buf[idx] = r.nextTok
		r.count++
	}
	return true
}

// TryConsume removes all n elements or none. It returns false (and changes no
// state) on underflow (n > count). n >= 1 is validated by bp. Advancing the
// head pointer moves/copies zero elements.
func (r *Ring) TryConsume(n int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.moved = 0
	if n > r.count {
		return false // underflow: nothing is removed
	}
	r.head = (r.head + n) % r.capacity
	r.count -= n
	return true
}

// Count returns the current number of elements.
func (r *Ring) Count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// Free returns the remaining capacity.
func (r *Ring) Free() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.capacity - r.count
}

// Capacity returns the fixed capacity.
func (r *Ring) Capacity() int64 {
	return r.capacity
}
