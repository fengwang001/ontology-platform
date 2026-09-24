// Package mlfq implements a gaming-resistant MLFQ scheduler over integer ticks.
package mlfq

import (
	"errors"
	"ontology/level"
	"sync"
)

var ErrDuplicate = errors.New("mlfq: duplicate job id")
var ErrUnknown = errors.New("mlfq: unknown job id")
var ErrFull = errors.New("mlfq: job limit reached")

// Config: L levels, per-level quotas Q, boost period S, job cap MaxJobs.
type Config struct {
	Levels, Boost, MaxJobs int
	Quotas                 []int
}

// Stats counters; LastCheck = queues inspected by the most recent Step.
type Stats struct{ Active, Clock, LastCheck int }
type job struct{ remain, level, used int } // used: ticks spent at current level
type Scheduler struct {
	mu          sync.Mutex
	cfg         Config
	qs          []level.Queue
	jobs        map[int]*job
	clock, last int
}

func New(cfg Config) *Scheduler {
	return &Scheduler{cfg: cfg, qs: make([]level.Queue, cfg.Levels), jobs: map[int]*job{}}
}
func (s *Scheduler) Submit(id, work int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.jobs[id]; dup {
		return ErrDuplicate
	} else if len(s.jobs) >= s.cfg.MaxJobs {
		return ErrFull
	}
	s.jobs[id] = &job{remain: work}
	s.qs[0].Push(id)
	return nil
}

// Step runs the head job of the highest non-empty level for one tick.
func (s *Scheduler) Step() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lv := 0
	for ; lv < len(s.qs) && s.qs[lv].Len() == 0; lv++ {
	}
	if s.last = min(lv+1, len(s.qs)); lv == len(s.qs) {
		return 0, false
	}
	id, _ := s.qs[lv].Front()
	j := s.jobs[id]
	j.remain, j.used, s.clock = j.remain-1, j.used+1, s.clock+1
	if j.remain == 0 {
		s.qs[lv].Pop()
		delete(s.jobs, id)
	} else if j.used >= s.cfg.Quotas[lv] {
		s.qs[lv].Pop()
		j.used = 0
		j.level = min(lv+1, len(s.qs)-1)
		s.qs[j.level].Push(id)
	}
	if s.clock%s.cfg.Boost == 0 {
		s.boost()
	}
	return id, true
}
func (s *Scheduler) Yield(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; !ok {
		return ErrUnknown
	} else if front, _ := s.qs[j.level].Front(); front == id {
		s.qs[j.level].Pop()
		s.qs[j.level].Push(id)
	}
	return nil
}
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{len(s.jobs), s.clock, s.last}
}
func (s *Scheduler) boost() { // returns all jobs to top level, clears quotas
	for i := 1; i < len(s.qs); i++ {
		for id, ok := s.qs[i].Pop(); ok; id, ok = s.qs[i].Pop() {
			s.jobs[id].level = 0
			s.qs[0].Push(id)
		}
	}
	for _, j := range s.jobs {
		j.used = 0
	}
}
