package drr

import (
	"errors"
	"sync"

	"ontology/flowq"
)

const MaxBlock, MaxQueued = 1500, 1_000_000

var (
	ErrUnknownFlow = errors.New("unknown flow")
	ErrBadSize     = errors.New("bad block size")
	ErrFull        = errors.New("queue is full")
	ErrExists      = errors.New("flow already exists")
)

type FlowStats struct{ ID, Quantum, QueuedBlocks, QueuedBytes, Deficit, EnqueuedBytes, SentBytes int }
type flow struct{ id, quantum, deficit, queued, enqueued, sent int; q *flowq.Queue }

type Scheduler struct {
	mu sync.Mutex
	flows map[int]*flow
	active []int
	head int
	current bool
	queued, checks int
}

func New() *Scheduler { return &Scheduler{flows: map[int]*flow{}} }

func (s *Scheduler) AddFlow(id, quantum int) error {
	if quantum <= 0 {
		return ErrBadSize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.flows[id]; ok {
		return ErrExists
	}
	s.flows[id] = &flow{id: id, quantum: quantum, q: flowq.New()}
	return nil
}

func (s *Scheduler) Enqueue(id, size int) error {
	if size <= 0 || size > MaxBlock {
		return ErrBadSize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flows[id]
	if !ok {
		return ErrUnknownFlow
	}
	if s.queued == MaxQueued {
		return ErrFull
	}
	if f.queued == 0 {
		s.active = append(s.active, id)
	}
	f.q.Push(size)
	f.queued, f.enqueued, s.queued = f.queued+1, f.enqueued+size, s.queued+1
	return nil
}

func (s *Scheduler) Stats() []FlowStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FlowStats
	for _, f := range s.flows {
		out = append(out, FlowStats{f.id, f.quantum, f.queued, f.q.Bytes(), f.deficit, f.enqueued, f.sent})
	}
	return out
}

func (s *Scheduler) FlowChecks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checks
}
