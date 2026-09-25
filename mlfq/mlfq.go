// Package mlfq 实现防博弈的多级反馈队列调度器。
package mlfq

import (
	"errors"
	"sync"

	"ontology/level"
)

var ErrDuplicate, ErrUnknown, ErrFull = errors.New("mlfq: duplicate job id"), errors.New("mlfq: unknown job id"), errors.New("mlfq: job limit reached")

type Config struct {
	Levels, Boost, MaxJobs int
	Quotas                 []int
}
type Stats struct{ Ticks, Active, Completed, Checked int }
type job struct{ left, lvl, used int }
type Scheduler struct {
	mu                  sync.Mutex
	cfg                 Config
	qs                  []level.Queue
	jobs                map[int]*job
	tick, done, checked int
}

func New(cfg Config) *Scheduler {
	return &Scheduler{cfg: cfg, qs: make([]level.Queue, cfg.Levels), jobs: map[int]*job{}}
}
func (s *Scheduler) Submit(id, work int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; ok {
		return ErrDuplicate
	}
	if len(s.jobs) >= s.cfg.MaxJobs {
		return ErrFull
	}
	s.jobs[id] = &job{left: work}
	s.qs[0].Push(id)
	return nil
}
func (s *Scheduler) Step() (id int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lvl := 0
	for lvl < len(s.qs) && s.qs[lvl].Len() == 0 {
		lvl++
	}
	s.checked = min(lvl+1, len(s.qs))
	if lvl == len(s.qs) {
		return 0, false
	}
	id = s.qs[lvl].Front()
	j := s.jobs[id]
	j.left, j.used, s.tick = j.left-1, j.used+1, s.tick+1
	if j.left == 0 {
		s.qs[lvl].Pop()
		delete(s.jobs, id)
		s.done++
	} else if j.used >= s.cfg.Quotas[lvl] {
		s.qs[lvl].Pop()
		j.used, j.lvl = 0, min(lvl+1, len(s.qs)-1)
		s.qs[j.lvl].Push(id)
	}
	if s.cfg.Boost > 0 && s.tick%s.cfg.Boost == 0 {
		s.boost()
	}
	return id, true
}

// Yield 让 id 作业主动让出：若它在本级队首则回到队尾，配额账不清零。
func (s *Scheduler) Yield(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return ErrUnknown
	}
	if q := &s.qs[j.lvl]; q.Front() == id {
		q.Push(q.Pop())
	}
	return nil
}
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{s.tick, len(s.jobs), s.done, s.checked}
}
func (s *Scheduler) boost() {
	for i := 1; i < len(s.qs); i++ {
		for s.qs[i].Len() > 0 {
			s.qs[0].Push(s.qs[i].Pop())
		}
	}
	for _, j := range s.jobs {
		j.lvl, j.used = 0, 0
	}
}
