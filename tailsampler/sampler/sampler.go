// Package sampler implements a tail sampler with a decision cache and a
// keep quota. Out-of-order spans are buffered per trace; once a trace goes
// silent, hits its span cap, or is evicted, it is decided by a fixed policy
// order. Late spans follow the cached decision. A single mutex makes all
// concurrent calls equivalent to some serial order, so replaying the same
// input sequence always yields the same decision list.
package sampler

import (
	"container/heap"
	"sync"

	"ontology/tailsampler/policy"
	"ontology/tailsampler/span"
)

// Sampler is safe for concurrent use.
type Sampler struct {
	mu   sync.Mutex
	p    Params
	hash policy.HashFunc

	maxNow uint64
	hasNow bool

	buf   map[string]*bufItem
	bheap bufHeap
	cache *decisionCache // nil when disabled (Td==0 or Cmax==0)

	lastWin uint64
	hasWin  bool
	used    uint64 // keeps consumed in the current quota window

	lateKept    uint64
	lateDropped uint64

	tickExamined uint64 // buffer items inspected by Tick (test assertion aid)
}

// Ingest accepts one span. It returns the decisions taken as a side effect
// (buffer eviction first, then the Sc-triggered decision), in decision order.
func (s *Sampler) Ingest(now uint64, traceID, spanID string, durMs uint64, isErr bool) ([]Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if traceID == "" || spanID == "" || durMs > maxDurMs || now > maxClock {
		return nil, ErrInvalidParam
	}
	if s.hasNow && now < s.maxNow {
		return nil, ErrClock
	}
	if s.cache != nil {
		if keep, ok := s.cache.lookup(traceID, now); ok {
			s.acceptClock(now)
			if keep {
				s.lateKept++
			} else {
				s.lateDropped++
			}
			return nil, nil
		}
	}
	if it, ok := s.buf[traceID]; ok && it.entry.Has(spanID) {
		return nil, ErrDuplicate
	}
	s.acceptClock(now)

	var out []Decision
	it, ok := s.buf[traceID]
	if !ok {
		if uint64(len(s.buf)) >= s.p.Nmax {
			victim := heap.Pop(&s.bheap).(*bufItem)
			delete(s.buf, victim.traceID)
			out = append(out, s.decide(victim.traceID, victim.entry, now, true))
		}
		it = &bufItem{traceID: traceID, entry: span.New()}
		heap.Push(&s.bheap, it)
		s.buf[traceID] = it
	}
	it.entry.Add(spanID, durMs, isErr)
	it.entry.LastSeen = now
	heap.Fix(&s.bheap, it.idx)
	if uint64(it.entry.Count()) >= s.p.Sc {
		s.remove(it)
		out = append(out, s.decide(traceID, it.entry, now, false))
	}
	return out, nil
}

// Tick decides every buffered trace that has gone silent
// (lastSeen+W <= now) in ascending (lastSeen, traceID) order.
func (s *Sampler) Tick(now uint64) ([]Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now > maxClock {
		return nil, ErrInvalidParam
	}
	if s.hasNow && now < s.maxNow {
		return nil, ErrClock
	}
	s.acceptClock(now)

	var out []Decision
	examined := uint64(0)
	for len(s.bheap) > 0 {
		top := s.bheap[0]
		examined++
		if top.entry.LastSeen+s.p.W > now {
			break
		}
		heap.Pop(&s.bheap)
		delete(s.buf, top.traceID)
		out = append(out, s.decide(top.traceID, top.entry, now, false))
	}
	s.tickExamined += examined
	return out, nil
}

// decide runs the policy, applies the quota window, writes the cache and
// returns the Decision. The caller must have removed the entry already.
func (s *Sampler) decide(traceID string, e *span.Entry, now uint64, evicted bool) Decision {
	keep, reason := policy.Decide(traceID, e.SeenErr(), e.MaxDur(), s.p.L, s.p.P, s.hash)
	win := now / s.p.Wb
	if !s.hasWin || win != s.lastWin {
		s.used = 0
		s.lastWin = win
		s.hasWin = true
	}
	if keep {
		if reason == policy.ReasonProb {
			if s.used < s.p.Q {
				s.used++
			} else {
				keep, reason = false, policy.ReasonBudget
			}
		} else {
			s.used++ // Error/Latency keeps always succeed, may exceed Q
		}
	}
	if s.cache != nil {
		s.cache.store(traceID, keep, now)
	}
	return Decision{TraceID: traceID, Keep: keep, Reason: reason, Spans: e.Count(), At: now, Evicted: evicted}
}

func (s *Sampler) remove(it *bufItem) {
	heap.Remove(&s.bheap, it.idx)
	delete(s.buf, it.traceID)
}

func (s *Sampler) acceptClock(now uint64) {
	s.maxNow = now
	s.hasNow = true
}

// LateKept returns the count of late spans released by a cached keep.
func (s *Sampler) LateKept() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lateKept
}

// LateDropped returns the count of late spans dropped by a cached drop.
func (s *Sampler) LateDropped() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lateDropped
}
