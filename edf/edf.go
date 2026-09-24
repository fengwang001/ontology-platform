package edf

import (
	"container/heap"
	"errors"
	"math/big"
	"sync"

	"ontology/task"
)

const MaxHyperperiod = 10000

var (
	ErrInvalid     = errors.New("invalid periodic task")
	ErrOverload    = errors.New("scheduler overload")
	ErrHyperperiod = errors.New("hyperperiod exceeds limit")
)

type Miss struct {
	TaskID   string
	Deadline int
}

type Scheduler struct {
	mu          sync.Mutex
	tasks       map[string]task.Task
	hyperperiod int
	load        *big.Rat
	decision    int64
}

func New() *Scheduler {
	return &Scheduler{tasks: map[string]task.Task{}, hyperperiod: 1, load: new(big.Rat)}
}

func (s *Scheduler) Admit(t task.Task) error {
	if !t.Valid() {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[t.ID]; exists {
		return ErrInvalid
	}
	nextLoad := new(big.Rat).Add(s.load, util(t))
	if nextLoad.Cmp(new(big.Rat).SetInt64(1)) > 0 {
		return ErrOverload
	}
	nextHyper := lcm(s.hyperperiod, t.T)
	if nextHyper > MaxHyperperiod || nextHyper <= 0 {
		return ErrHyperperiod
	}
	s.tasks[t.ID] = t
	s.load = nextLoad
	s.hyperperiod = nextHyper
	return nil
}

func (s *Scheduler) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
	s.load = new(big.Rat)
	s.hyperperiod = 1
	for _, t := range s.tasks {
		s.load.Add(s.load, util(t))
		s.hyperperiod = lcm(s.hyperperiod, t.T)
	}
}

func (s *Scheduler) Hyperperiod() int { s.mu.Lock(); defer s.mu.Unlock(); return s.hyperperiod }
func (s *Scheduler) LastDecisionComparisons() int64 { s.mu.Lock(); defer s.mu.Unlock(); return s.decision }

func (s *Scheduler) Tasks() []task.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := make([]task.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	return tasks
}

func (s *Scheduler) Simulate(horizon int) []Miss {
	s.mu.Lock()
	defer s.mu.Unlock()
	if horizon < 0 {
		return nil
	}
	ready := &jobHeap{s: s}
	var misses []Miss
	expire := func(now int) {
		for ready.Len() > 0 && ready.jobs[0].deadline <= now {
			job := heap.Pop(ready).(*job)
			if job.remaining > 0 {
				misses = append(misses, Miss{job.id, job.deadline})
			}
		}
	}
	for now := 0; now < horizon; now++ {
		expire(now)
		for _, t := range s.tasks {
			if now%t.T == 0 {
				heap.Push(ready, &job{id: t.ID, deadline: now + t.T, remaining: t.C})
			}
		}
		s.decision = 0
		if ready.Len() > 0 {
			ready.jobs[0].remaining--
			if ready.jobs[0].remaining == 0 {
				heap.Pop(ready)
			}
		}
	}
	expire(horizon)
	return misses
}

type job struct {
	id        string
	deadline  int
	remaining int
}

type jobHeap struct {
	jobs []*job
	s    *Scheduler
}

func (h *jobHeap) Len() int { return len(h.jobs) }
func (h *jobHeap) Less(i, j int) bool {
	h.s.decision++
	return h.jobs[i].deadline < h.jobs[j].deadline ||
		(h.jobs[i].deadline == h.jobs[j].deadline && h.jobs[i].id < h.jobs[j].id)
}
func (h *jobHeap) Swap(i, j int)       { h.jobs[i], h.jobs[j] = h.jobs[j], h.jobs[i] }
func (h *jobHeap) Push(x any)          { h.jobs = append(h.jobs, x.(*job)) }
func (h *jobHeap) Pop() any            { x := h.jobs[len(h.jobs)-1]; h.jobs = h.jobs[:len(h.jobs)-1]; return x }

func lcm(a, b int) int {
	g := a
	for x, y := a, b; y != 0; x, y = y, x%y {
		g = y
	}
	return a / g * b
}

func util(t task.Task) *big.Rat {
	return new(big.Rat).SetFrac(big.NewInt(int64(t.C)), big.NewInt(int64(t.T)))
}
