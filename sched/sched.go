// Package sched provides a bounded ready queue for task scheduling.
package sched

import "container/heap"

// Scheduler tracks readiness, in-flight counts and decision counters.
// It is driven by a single goroutine; no internal locking is required.
type Scheduler struct {
	indeg     map[string]int
	pq        idHeap
	active    int
	limit     int
	decisions int
	peak      int
}

type idHeap []string

func (h idHeap) Len() int           { return len(h) }
func (h idHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h idHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x any)        { *h = append(*h, x.(string)) }
func (h *idHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// New builds a scheduler. indeeg[v] is the number of pending predecessors;
// limit bounds concurrently running tasks (values < 1 mean 1).
func New(indeg map[string]int, limit int) *Scheduler {
	if limit < 1 {
		limit = 1
	}
	s := &Scheduler{indeg: map[string]int{}, limit: limit}
	for id, n := range indeg {
		s.indeg[id] = n
		if n == 0 {
			heap.Push(&s.pq, id)
		}
	}
	return s
}

// Next returns the next runnable task id and true.
// Every call counts as one readiness decision.
func (s *Scheduler) Next() (string, bool) {
	s.decisions++
	if s.active >= s.limit || s.pq.Len() == 0 {
		return "", false
	}
	id := heap.Pop(&s.pq).(string)
	s.active++
	if s.active > s.peak {
		s.peak = s.active
	}
	return id, true
}

// Release marks a finished task and enqueues successors whose deps are done.
// It is called exactly once per launched task.
func (s *Scheduler) Release(id string, successors []string) (enqueued []string) {
	s.active--
	for _, v := range successors {
		s.indeg[v]--
		if s.indeg[v] == 0 {
			heap.Push(&s.pq, v)
			enqueued = append(enqueued, v)
		}
	}
	return enqueued
}

// Active reports the number of currently running tasks.
func (s *Scheduler) Active() int { return s.active }

// Waiting reports tasks never launched (still queued or blocked), for skip marking.
func (s *Scheduler) Waiting() []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range s.pq {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for id, n := range s.indeg {
		if n > 0 && !seen[id] {
			out = append(out, id)
		}
	}
	return out
}

// Peak returns the historical maximum number of concurrently running tasks.
func (s *Scheduler) Peak() int { return s.peak }

// Decisions returns the total number of readiness decisions (Next calls).
func (s *Scheduler) Decisions() int { return s.decisions }

// Limit returns the configured concurrency limit.
func (s *Scheduler) Limit() int { return s.limit }
