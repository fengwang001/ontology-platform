// Package sched provides the concurrency limiter, the deterministic ready
// queue and the instrumentation counters used by the execution engine.
package sched

import (
	"sort"
	"sync"
)

// Limiter bounds the number of simultaneously running tasks. It records the
// historical peak (unexported) and the number of readiness decisions.
type Limiter struct {
	mu      sync.Mutex
	tokens  chan struct{}
	running int
	peak    int
	decide  int
}

// NewLimiter creates a limiter with the given maximum concurrency. Values
// below 1 are clamped to 1.
func NewLimiter(max int) *Limiter {
	if max < 1 {
		max = 1
	}
	return &Limiter{tokens: make(chan struct{}, max)}
}

// Acquire blocks until a slot is available.
func (l *Limiter) Acquire() {
	l.tokens <- struct{}{}
	l.mu.Lock()
	l.running++
	if l.running > l.peak {
		l.peak = l.running
	}
	l.mu.Unlock()
}

// Release frees one slot.
func (l *Limiter) Release() {
	l.mu.Lock()
	l.running--
	l.mu.Unlock()
	<-l.tokens
}

// Peak returns the highest concurrently-running count observed so far.
func (l *Limiter) Peak() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.peak
}

// Decision records one readiness decision (one edge being considered).
func (l *Limiter) Decision(n int) {
	l.mu.Lock()
	l.decide += n
	l.mu.Unlock()
}

// Decisions returns the readiness-decision counter.
func (l *Limiter) Decisions() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.decide
}

// ReadyQueue yields ready tasks in ascending id order. Push is used only when
// a batch of newly ready tasks is known, so popping keeps ordering stable
// regardless of completion timing.
type ReadyQueue struct {
	ids []string
}

// NewReadyQueue returns an empty queue.
func NewReadyQueue() *ReadyQueue { return &ReadyQueue{} }

// PushAll adds ids and re-sorts, so ties break by ascending id.
func (q *ReadyQueue) PushAll(ids []string) {
	q.ids = append(q.ids, ids...)
	sort.Strings(q.ids)
}

// Pop removes and returns the smallest id, or "" when empty.
func (q *ReadyQueue) Pop() string {
	if len(q.ids) == 0 {
		return ""
	}
	id := q.ids[0]
	q.ids = q.ids[1:]
	return id
}

// Len reports queued items.
func (q *ReadyQueue) Len() int { return len(q.ids) }
