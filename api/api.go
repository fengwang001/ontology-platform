// Package api is the concurrency-safe public WFQ surface (api -> wfq -> flow, one-way deps).
package api

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"

	"ontology/wfq"
)

// Three distinct, judgeable sentinel errors.
var (
	ErrConfig = wfq.ErrConfig // empty weights or a weight <= 0
	ErrSubmit = wfq.ErrSubmit // flow out of range or size <= 0
	ErrEmpty  = wfq.ErrEmpty  // Dequeue when every queue is empty
)

// WFQ is the concurrency-safe weighted fair queueing scheduler.
type WFQ struct {
	mu sync.Mutex
	s  *wfq.Scheduler
}

// New creates one flow per weight (non-empty, every weight >= 1).
func New(weights []int) (*WFQ, error) {
	s, err := wfq.New(weights)
	if err != nil {
		return nil, err
	}
	return &WFQ{s: s}, nil
}
func (q *WFQ) Submit(flow, size int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.s.Submit(flow, size)
}
func (q *WFQ) Dequeue() (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.s.Dequeue()
}
func (q *WFQ) VirtualTime() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.s.VirtualTime()
}

// SelfCheck verifies the four invariants on built-in deterministic
// sequences: naive agreement (also F monotonicity/in-queue order), fairness, no-trace reject.
func (q *WFQ) SelfCheck() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return checkNaive() && checkFair() && checkRejection()
}

// checkNaive drives the scheduler and a naive O(n) scan with one random
// stream, comparing F, every per-flow finish queue and V after each step.
func checkNaive() bool {
	rng := rand.New(rand.NewSource(1))
	w := []int{1, 2, 3, 5}
	n := len(w)
	s, _ := wfq.New(w)
	F := make([]int, n)
	qs := [][]int{{}, {}, {}, {}} // non-nil empties, matching FlowQueue snapshots
	V, live := 0, 0
	for i := 0; i < 300 || live > 0; i++ {
		if live == 0 || (i < 300 && rng.Intn(2) == 0) {
			f, sz := rng.Intn(n), rng.Intn(9)+1
			if s.Submit(f, sz) != nil {
				return false
			}
			F[f] = max(F[f], V) + (sz+w[f]-1)/w[f]
			qs[f], live = append(qs[f], F[f]), live+1
		} else {
			got, err := s.Dequeue()
			want := -1 // ascending scan: strict `<` keeps the smaller index on ties
			for f := 0; f < n; f++ {
				if len(qs[f]) > 0 && (want < 0 || qs[f][0] < qs[want][0]) {
					want = f
				}
			}
			if err != nil || got != want {
				return false
			}
			V, qs[want], live = qs[want][0], qs[want][1:], live-1
		}
		for f := -1; f < n; f++ {
			if (f < 0 && s.VirtualTime() != V) || (f >= 0 &&
				(s.FlowFinish(f) != F[f] || !reflect.DeepEqual(s.FlowQueue(f), qs[f]))) {
				return false
			}
		}
	}
	return true
}

// checkFair: equal-size packets dequeue in descending-weight order.
func checkFair() bool {
	fair, _ := wfq.New([]int{4, 2, 1}) // size 8 -> finishes 2,4,8
	for f := range 3 {
		if fair.Submit(f, 8) != nil {
			return false
		}
	}
	for want := range 3 {
		if got, err := fair.Dequeue(); err != nil || got != want {
			return false
		}
	}
	return true
}

// checkRejection: three distinct sentinels; rejected calls leave V/F/queues untouched and use continues.
func checkRejection() bool {
	for _, badW := range [][]int{nil, {1, 0}} {
		if q, err := New(badW); q != nil || !errors.Is(err, ErrConfig) {
			return false
		}
	}
	if errors.Is(ErrSubmit, ErrEmpty) || errors.Is(ErrConfig, ErrSubmit) || errors.Is(ErrConfig, ErrEmpty) {
		return false
	}
	q, _ := New([]int{2, 1})
	snap := q.snap()
	for _, c := range [][2]int{{-1, 1}, {9, 1}, {0, 0}, {1, -3}} {
		if !errors.Is(q.Submit(c[0], c[1]), ErrSubmit) || !reflect.DeepEqual(q.snap(), snap) {
			return false
		}
	}
	if _, err := q.Dequeue(); !errors.Is(err, ErrEmpty) || !reflect.DeepEqual(q.snap(), snap) {
		return false
	}
	if q.Submit(0, 2) != nil || q.Submit(1, 1) != nil {
		return false
	}
	f1, e1 := q.Dequeue()
	f2, e2 := q.Dequeue()
	return e1 == nil && f1 == 0 && e2 == nil && f2 == 1
}

// snap serializes V and every flow's F and finish queue for comparison.
func (q *WFQ) snap() [][]int {
	out := [][]int{{q.s.VirtualTime()}}
	for f := range 2 {
		out = append(out, append([]int{q.s.FlowFinish(f)}, q.s.FlowQueue(f)...))
	}
	return out
}
