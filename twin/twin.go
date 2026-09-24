// Package twin implements a FIFO queue over two aggregate stacks: new values
// land on in, evictions leave from out. A flip moves every element of in onto
// out one at a time, reversing their order, so out's top is always the oldest
// element. Each element moves at most once per flip cycle, giving amortized
// O(1) Push/Evict/Max. It depends only on stk.
package twin

import "ontology/stk"

// Queue is the two-stack window core. The zero value is NOT ready; use New.
// It is not safe for concurrent use; the api package serializes access.
type Queue struct {
	in  *stk.Stack
	out *stk.Stack

	// moves counts elements transferred during flips. It is deliberately
	// unexported and never readable through the public api; white-box tests in
	// this package read it directly.
	moves int
}

// New returns an empty queue.
func New() *Queue { return &Queue{in: stk.New(), out: stk.New()} }

// Len is the number of elements currently held.
func (q *Queue) Len() int { return q.in.Len() + q.out.Len() }

// Push appends v to the window tail (the in stack). Callers trigger eviction
// separately; Push never evicts by itself.
func (q *Queue) Push(v float64) { q.in.Push(v) }

// flip reverses in into out, recomputing aggregates under out's rule. It runs
// only when out is empty and an eviction is required.
func (q *Queue) flip() {
	for q.in.Len() > 0 {
		v, _ := q.in.Pop()
		q.out.Push(v)
		q.moves++
	}
}

// Evict removes and returns the window head (oldest value). If out is empty it
// first flips all of in into out; if out already holds elements there is no
// flip. It returns false without changing state on an empty queue.
func (q *Queue) Evict() (float64, bool) {
	if q.Len() == 0 {
		return 0, false
	}
	if q.out.Len() == 0 {
		q.flip()
	}
	return q.out.Pop()
}

// Max returns the window maximum from the two stack-top aggregates. Empty
// stacks do not participate; it returns false on an empty queue.
func (q *Queue) Max() (float64, bool) {
	m, seen := 0.0, false
	if a, ok := q.in.TopAgg(); ok {
		m, seen = a, true
	}
	if a, ok := q.out.TopAgg(); ok && (!seen || a > m) {
		m, seen = a, true
	}
	return m, seen
}

// Values returns the contents from oldest to newest: out's top is the oldest,
// so out is read top-to-bottom (reversed), then in bottom-to-top.
func (q *Queue) Values() []float64 {
	oi, ii := q.out.Entries(), q.in.Entries()
	vs := make([]float64, 0, len(oi)+len(ii))
	for i := len(oi) - 1; i >= 0; i-- {
		vs = append(vs, oi[i].Value)
	}
	for _, e := range ii {
		vs = append(vs, e.Value)
	}
	return vs
}

// AggregatesValid reports invariant 3: every entry's aggregate equals the max
// of its own value and all values beneath it within the same stack.
func (q *Queue) AggregatesValid() bool {
	check := func(es []stk.Entry) bool {
		var cur float64
		for i, e := range es {
			if i == 0 {
				cur = e.Value
			} else if e.Value > cur {
				cur = e.Value
			}
			if e.Agg != cur {
				return false
			}
		}
		return true
	}
	return check(q.in.Entries()) && check(q.out.Entries())
}
