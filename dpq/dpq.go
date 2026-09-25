// Package dpq is a thread-safe double-ended priority queue built on
// the mmheap min-max heap. The zero value is ready to use.
package dpq

import (
	"sync"

	"ontology/mmheap"
)

// Queue supports concurrent Push/Min/Max/DeleteMin/DeleteMax/Len.
type Queue struct {
	mu sync.Mutex
	h  mmheap.Heap
}

func (q *Queue) Push(v int64) { q.mu.Lock(); defer q.mu.Unlock(); q.h.Push(v) }

func (q *Queue) Min() (int64, bool) { q.mu.Lock(); defer q.mu.Unlock(); return q.h.Min() }

func (q *Queue) Max() (int64, bool) { q.mu.Lock(); defer q.mu.Unlock(); return q.h.Max() }

func (q *Queue) DeleteMin() (int64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.h.DeleteMin()
}

func (q *Queue) DeleteMax() (int64, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.h.DeleteMax()
}

func (q *Queue) Len() int { q.mu.Lock(); defer q.mu.Unlock(); return q.h.Len() }

func (q *Queue) Empty() bool { return q.Len() == 0 }

// Check verifies the heap structure invariant.
func (q *Queue) Check() error { q.mu.Lock(); defer q.mu.Unlock(); return q.h.Check() }

// ScaleOK reports whether the last update walked a heap path (see mmheap).
func (q *Queue) ScaleOK(m int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.h.ScaleOK(m)
}
