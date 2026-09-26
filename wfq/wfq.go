// Package wfq is the weighted fair queueing scheduler: the flow set, the
// virtual clock V and a min-heap locating the non-empty flow whose head
// packet has the smallest finish (ties: smallest flow index).
package wfq

import (
	"container/heap"
	"errors"

	"ontology/flow"
)

// Sentinel errors for the three distinguishable failure classes.
var (
	ErrConfig = errors.New("wfq: invalid weights configuration") // empty / <=0
	ErrSubmit = errors.New("wfq: invalid submit arguments")      // bad flow / size
	ErrEmpty  = errors.New("wfq: all flow queues are empty")
)

// Scheduler is the WFQ scheduler. Use New; the zero value is not usable.
type Scheduler struct {
	flows []*flow.Flow
	h     heapHeap // at most one entry per flow, keyed by its head finish
	vtime int      // system virtual clock V
	// probes: flows inspected during the most recent Dequeue. Unexported;
	// white-box tests are its only readers, no exported method returns it.
	probes int
}

// New creates n flows. weights must be non-empty and every weight >= 1.
func New(weights []int) (*Scheduler, error) {
	if len(weights) == 0 {
		return nil, ErrConfig
	}
	flows := make([]*flow.Flow, len(weights))
	for i, w := range weights {
		if w < 1 {
			return nil, ErrConfig
		}
		f, err := flow.New(w)
		if err != nil {
			return nil, ErrConfig
		}
		flows[i] = f
	}
	s := &Scheduler{flows: flows}
	heap.Init(&s.h)
	return s, nil
}

// Submit enqueues a packet of the given byte size onto flow. It validates
// before mutating, so a rejected call leaves no trace.
func (s *Scheduler) Submit(flowID, size int) error {
	if flowID < 0 || flowID >= len(s.flows) || size < 1 {
		return ErrSubmit
	}
	f := s.flows[flowID]
	wasEmpty := f.Len() == 0
	f.Enqueue(size, s.vtime)
	// New packets append at the tail and never change the head key, so a
	// non-empty flow needs no heap update; only an empty flow enters.
	if wasEmpty {
		heap.Push(&s.h, heapItem{flow: flowID, finish: f.Head().Finish})
	}
	return nil
}

// Dequeue returns the non-empty flow whose head finish is smallest (ties:
// smallest index) and advances V to that packet's finish time.
func (s *Scheduler) Dequeue() (int, error) {
	if s.h.Len() == 0 {
		s.probes = 0
		return 0, ErrEmpty
	}
	// The heap root alone identifies the minimum: one flow inspected,
	// independent of the number of flows.
	s.probes = 1
	item := heap.Pop(&s.h).(heapItem)
	f := s.flows[item.flow]
	s.vtime = f.Pop().Finish
	if f.Len() > 0 {
		heap.Push(&s.h, heapItem{flow: item.flow, finish: f.Head().Finish})
	}
	return item.flow, nil
}

// VirtualTime returns the virtual clock V.
func (s *Scheduler) VirtualTime() int { return s.vtime }

// FlowFinish returns flow i's finish counter F_i.
func (s *Scheduler) FlowFinish(i int) int { return s.flows[i].Finish() }

// FlowLen returns the number of packets queued on flow i.
func (s *Scheduler) FlowLen(i int) int { return s.flows[i].Len() }

// FlowQueue returns a snapshot copy of flow i's queued finish times.
func (s *Scheduler) FlowQueue(i int) []int {
	n := s.flows[i].Len()
	out := make([]int, 0, n)
	for j := 0; j < n; j++ {
		out = append(out, s.flows[i].At(j).Finish)
	}
	return out
}

// ProbesBounded reports whether minimum selection inspects at most bound
// flows regardless of flow count; it returns only a verdict, never the count.
func (s *Scheduler) ProbesBounded(ms []int, bound int) bool {
	for _, m := range ms {
		weights := make([]int, m)
		for i := range weights {
			weights[i] = 1
		}
		big, err := New(weights)
		if err != nil {
			return false
		}
		for i := 0; i < m; i++ {
			if err := big.Submit(i, m-i); err != nil { // sizes distinct
				return false
			}
		}
		if _, err := big.Dequeue(); err != nil || big.probes > bound {
			return false
		}
	}
	return true
}

// heapItem: one per non-empty flow; finish is its head packet's finish.
type heapItem struct{ flow, finish int }

type heapHeap []heapItem

func (h heapHeap) Len() int { return len(h) }
func (h heapHeap) Less(i, j int) bool {
	if h[i].finish != h[j].finish {
		return h[i].finish < h[j].finish
	}
	return h[i].flow < h[j].flow // tie: smallest flow index
}
func (h heapHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *heapHeap) Push(x any)   { *h = append(*h, x.(heapItem)) }
func (h *heapHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
