// Package sched is the non-preemptive SJF core: ready set, binary min-heap
// selection, clock advancement and completion order.
package sched

import (
	"container/heap"
	"errors"
	"math/bits"
	"sort"
	"strconv"
	"sync"

	"ontology/sjob"
)

var ErrDuplicateID = errors.New("sched: duplicate job id")

// ExecInterval is one non-preemptive CPU execution [Start, Finish).
type ExecInterval struct {
	ID            string
	Start, Finish int64
}

type Scheduler struct {
	mu          sync.Mutex
	jobs        map[string]*sjob.Job
	order, pend []*sjob.Job // registration order; admissions by (arrive, reg)
	ready       readyHeap
	clock       int64
	finish      []string
	trace       []ExecInterval
	inspect     int // private: nodes examined at the latest dispatch
}

func New() *Scheduler {
	s := new(Scheduler)
	s.jobs = map[string]*sjob.Job{}
	s.ready.probe = &s.inspect
	return s
}

// Add registers a job; it fails without changing state on a duplicate id.
func (s *Scheduler) Add(id string, arrive, length int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; ok {
		return ErrDuplicateID
	}
	j := sjob.New(id, arrive, length, len(s.order))
	s.jobs[id], s.order = j, append(s.order, j)
	return nil
}

// prepare rebuilds the admission queue from unfinished jobs.
func (s *Scheduler) prepare() {
	s.pend = s.pend[:0]
	for _, j := range s.order {
		if !j.Done() {
			s.pend = append(s.pend, j)
		}
	}
	sort.Slice(s.pend, func(i, k int) bool {
		a, b := s.pend[i], s.pend[k]
		return a.Arrive() < b.Arrive() || a.Arrive() == b.Arrive() && a.Reg() < b.Reg()
	})
	s.ready.jobs = nil
}

// next performs one idle-CPU dispatch: admit every job that has arrived by
// now (jumping an idle gap only if the ready set is empty), pop the (length,
// reg) minimum and run it to completion non-preemptively.
func (s *Scheduler) next() bool {
	if s.ready.Len() == 0 && len(s.pend) == 0 {
		return false
	}
	if s.ready.Len() == 0 && s.pend[0].Arrive() > s.clock {
		s.clock = s.pend[0].Arrive()
	}
	for len(s.pend) > 0 && s.pend[0].Arrive() <= s.clock {
		heap.Push(&s.ready, s.pend[0])
		s.pend = s.pend[1:]
	}
	s.inspect = 0
	j := heap.Pop(&s.ready).(*sjob.Job)
	f := j.Begin(s.clock) // non-preemptive: whole length in one step
	s.trace = append(s.trace, ExecInterval{j.ID(), s.clock, f})
	s.clock = f
	s.finish = append(s.finish, j.ID())
	return true
}

func (s *Scheduler) Run() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prepare()
	for len(s.finish) < len(s.order) && s.next() {
	}
	return append([]string(nil), s.finish...)
}

// Wait returns ticks from readiness until execution start, or -1.
func (s *Scheduler) Wait(id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		return j.Wait()
	}
	return -1
}

func (s *Scheduler) Trace() []ExecInterval {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExecInterval(nil), s.trace...)
}

// readyHeap orders ready jobs by (length, registration). probe counts Less
// calls during one Pop; it is private and never exposed by an exported method.
type readyHeap struct {
	jobs  []*sjob.Job
	probe *int
}

func (h readyHeap) Len() int { return len(h.jobs) }
func (h readyHeap) Less(i, k int) bool {
	*h.probe++
	return h.jobs[i].Less(h.jobs[k])
}
func (h readyHeap) Swap(i, k int) { h.jobs[i], h.jobs[k] = h.jobs[k], h.jobs[i] }
func (h *readyHeap) Push(x any)   { h.jobs = append(h.jobs, x.(*sjob.Job)) }
func (h *readyHeap) Pop() any     { x := h.jobs[len(h.jobs)-1]; h.jobs = h.jobs[:len(h.jobs)-1]; return x }

// HeapSelectionIsOlogM reports the O(log m) verdict (never the raw counter)
// for ready-set sizes 100, 1000 and 10000.
func HeapSelectionIsOlogM() bool {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			if s.Add("j"+strconv.Itoa(i), 0, int64(m-i)) != nil {
				return false
			}
		}
		s.prepare()
		if !s.next() || s.inspect > 4*bits.Len(uint(m)) {
			return false
		}
	}
	return true
}
