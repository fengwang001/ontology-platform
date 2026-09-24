package drr

import (
	"errors"
	"ontology/flowq"
	"sync"
)

const MaxBlock, MaxQueued = 1 << 20, 1 << 16

var ErrUnknownFlow = errors.New("unknown flow")
var ErrBadSize = errors.New("bad block size")
var ErrFull = errors.New("queue full")

type FlowStats struct {
	ID, Quantum, Queued, QueuedSize, Enqueued, Sent, Deficit int
	Active                                                   bool
}
type Scheduler struct {
	mu              sync.Mutex
	flows           map[int]*flow
	ring            *flow
	queued, checked int
}
type flow struct {
	id, quantum, deficit, sent int
	queue                      *flowq.Queue
	next, prev                 *flow
	credited                   bool
}

func New() *Scheduler { return &Scheduler{flows: map[int]*flow{}, ring: &flow{}} }

func (s *Scheduler) AddFlow(id, quantum int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if quantum <= 0 { return ErrBadSize }
	if s.flows[id] == nil {
		s.flows[id] = &flow{id: id, quantum: quantum, queue: flowq.New()}
	}
	return nil
}

func (s *Scheduler) Enqueue(id, size int) error {
	if size <= 0 || size > MaxBlock {
		return ErrBadSize
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if s.queued == MaxQueued { return ErrFull }
	if f == nil { return ErrUnknownFlow }
	f.queue.Enqueue(size)
	s.queued++
	if f.next == nil {
		if s.ring.next == nil {
			f.next, f.prev = f, f
			s.ring.next, s.ring.prev = f, f
			return nil
		}
		f.next, f.prev = s.ring, s.ring.prev
		s.ring.prev.next, s.ring.prev = f, f
	}
	return nil
}

func (s *Scheduler) Dequeue() (id, size int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = 0
	for f := s.ring.prev; f != s.ring; f = s.ring.prev {
		s.checked++
		if !f.credited {
			f.deficit, f.credited = f.deficit+f.quantum, true
		}
		size, _ = f.queue.Peek()
		if size > f.deficit {
			f.credited, s.ring.prev = false, f.next
			continue
		}
		f.queue.Dequeue()
		f.deficit, f.sent, s.queued = f.deficit-size, f.sent+size, s.queued-1
		if f.queue.Len() == 0 {
			f.prev.next, f.next.prev = f.next, f.prev
			s.ring.prev = f.prev
			f.deficit, f.credited, f.next, f.prev = 0, false, nil, nil
		}
		return f.id, size, true
	}
	return 0, 0, false
}

func (s *Scheduler) Stats() (out []FlowStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.flows {
		out = append(out, FlowStats{f.id, f.quantum, f.queue.Len(), f.queue.Bytes(), f.sent + f.queue.Bytes(), f.sent, f.deficit, f.next != nil})
	}
	return
}
