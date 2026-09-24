// Package drr 实现按字节公平的赤字轮转（DRR）：只有非空 flow 在活动轮转表里。
package drr

import (
	"errors"
	"ontology/flowq"
	"slices"
	"sort"
	"sync"
)

const MaxBlock, MaxQueued = 4096, 1 << 16 // 单块最大字节数；全局最大排队块数
var ErrUnknownFlow, ErrBadSize, ErrFull = errors.New("drr: unknown flow"), errors.New("drr: block size out of range"), errors.New("drr: queued block limit reached")

type FlowStat struct{ Sent, Queued, Enqueued int64 } // 单 flow 累计统计（字节）
type Stats struct {
	Flows   map[int]FlowStat
	Checked int
}

type flow struct {
	id, reg, quantum, deficit int
	q                         flowq.Queue
	sent, enqueued            int64
	active                    bool
}

type Scheduler struct {
	mu              sync.Mutex
	flows           map[int]*flow
	active          []*flow
	last            int
	queued, checked int
}

func New() *Scheduler { return &Scheduler{flows: map[int]*flow{}, last: -1} }
func (s *Scheduler) AddFlow(id, quantum int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.flows[id]; dup {
		return errors.New("drr: flow already exists")
	}
	s.flows[id] = &flow{id: id, reg: len(s.flows), quantum: quantum}
	return nil
}

func (s *Scheduler) Enqueue(id, size int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flows[id]
	if !ok {
		return ErrUnknownFlow
	}
	if size <= 0 || size > MaxBlock {
		return ErrBadSize
	}
	if s.queued >= MaxQueued {
		return ErrFull
	}
	if !f.active { // 空->非空：按注册序插入活动表
		f.active = true
		i := sort.Search(len(s.active), func(i int) bool { return s.active[i].reg > f.reg })
		s.active = slices.Insert(s.active, i, f)
	}
	f.q.Push(size)
	f.enqueued, s.queued = f.enqueued+int64(size), s.queued+1
	return nil
}

func (s *Scheduler) Dequeue() (id, size int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = 0
	for n := len(s.active); n > 0; n-- {
		i := sort.Search(len(s.active), func(i int) bool { return s.active[i].reg > s.last }) % len(s.active)
		f := s.active[i]
		s.checked, s.last, f.deficit = s.checked+1, f.reg, f.deficit+f.quantum
		for head, has := f.q.Front(); has && head <= f.deficit; head, has = f.q.Front() {
			head, _ = f.q.Pop()
			f.deficit, size, f.sent, s.queued = f.deficit-head, size+head, f.sent+int64(head), s.queued-1
		}
		if f.q.Len() == 0 {
			f.deficit, f.active = 0, false
			s.active = slices.Delete(s.active, i, i+1)
		}
		if size > 0 {
			return f.id, size, true
		}
	}
	return 0, 0, false
}
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Stats{Flows: make(map[int]FlowStat, len(s.flows)), Checked: s.checked}
	for id, f := range s.flows {
		st.Flows[id] = FlowStat{f.sent, int64(f.q.Bytes()), f.enqueued}
	}
	return st
}
