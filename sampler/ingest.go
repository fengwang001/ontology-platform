package sampler

import (
	"container/heap"

	"ontology/policy"
	"ontology/span"
)

// bufferNode pairs a buffered trace with its heap index.
type bufferNode struct {
	entry *span.Entry
	idx   int
}

// bufferHeap orders buffered traces by (lastSeen, traceID) ascending so that
// both silence ticks and capacity eviction touch the smallest element.
type bufferHeap []*bufferNode

func (h bufferHeap) Len() int { return len(h) }

func (h bufferHeap) Less(i, j int) bool {
	if h[i].entry.LastSeen() != h[j].entry.LastSeen() {
		return h[i].entry.LastSeen() < h[j].entry.LastSeen()
	}
	return h[i].entry.TraceID() < h[j].entry.TraceID()
}

func (h bufferHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}

func (h *bufferHeap) Push(x any) {
	n := x.(*bufferNode)
	n.idx = len(*h)
	*h = append(*h, n)
}

func (h *bufferHeap) Pop() any {
	old := *h
	n := len(old)
	node := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return node
}

// Ingest accepts one span and returns any decisions produced, in order.
func (s *Sampler) Ingest(now int64, traceID, spanID string, durMs int64, isErr bool) ([]Decision, error) {
	if traceID == "" || spanID == "" || durMs < 0 || durMs > 1e9 || now < 0 || now > 1e12 {
		return nil, errInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return nil, errClockRewind
	}
	if node, ok := s.buf[traceID]; ok {
		if !node.entry.Add(spanID, now, durMs, isErr) {
			return nil, errDuplicateSpan
		}
		heap.Fix(&s.order, node.idx) // lastSeen moved; restore heap order
		if node.entry.Spans() >= s.sc {
			heap.Remove(&s.order, node.idx)
			delete(s.buf, traceID)
			d := s.decideLocked(node.entry, now, false)
			s.clock = now
			return []Decision{d}, nil
		}
		s.clock = now
		return nil, nil
	}

	if ce, hit := s.dec.get(traceID, now); hit {
		// Live decision: follow it without buffering or extending the cache.
		if ce.keep {
			s.lateKept++
		} else {
			s.lateDropped++
		}
		s.clock = now
		return nil, nil
	}

	var out []Decision
	if int64(len(s.buf)) >= s.nmax {
		old := heap.Pop(&s.order).(*bufferNode)
		delete(s.buf, old.entry.TraceID())
		out = append(out, s.decideLocked(old.entry, now, true))
	}
	node := &bufferNode{entry: span.New(traceID)}
	node.entry.Add(spanID, now, durMs, isErr)
	s.buf[traceID] = node
	heap.Push(&s.order, node)
	if node.entry.Spans() >= s.sc {
		heap.Remove(&s.order, node.idx)
		delete(s.buf, traceID)
		out = append(out, s.decideLocked(node.entry, now, false))
	}
	s.clock = now
	return out, nil
}

// Tick forces silence-based decisions at time now.
func (s *Sampler) Tick(now int64) ([]Decision, error) {
	if now < 0 || now > 1e12 {
		return nil, errInvalidTick
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.clock {
		return nil, errClockRewind
	}
	s.tickInspected = 0
	s.tickDecided = 0
	var out []Decision
	for s.order.Len() > 0 {
		top := s.order[0]
		s.tickInspected++
		if top.entry.LastSeen()+s.w > now {
			break
		}
		heap.Pop(&s.order)
		delete(s.buf, top.entry.TraceID())
		out = append(out, s.decideLocked(top.entry, now, false))
		s.tickDecided++
	}
	s.clock = now
	return out, nil
}

// decideLocked applies policy then quota and records the decision. The entry
// must already have been removed from the buffer; caller holds s.mu.
func (s *Sampler) decideLocked(e *span.Entry, at int64, evicted bool) Decision {
	win := at / s.wb
	if win != s.window {
		s.window = win
		s.used = 0
	}
	keep, reason := s.pol.Decide(e.TraceID(), e.HasError(), e.MaxDur())
	if keep && reason == policy.ReasonProb && s.used >= s.q {
		keep, reason = false, ReasonBudget
	}
	if keep {
		s.used++
	}
	d := Decision{
		TraceID: e.TraceID(),
		Keep:    keep,
		Reason:  reason,
		Spans:   e.Spans(),
		At:      at,
		Evicted: evicted,
	}
	s.dec.put(decEntry{
		traceID:   d.TraceID,
		keep:      d.Keep,
		reason:    d.Reason,
		decidedAt: d.At,
	}, at)
	s.decisions++
	return d
}
