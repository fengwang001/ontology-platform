// Package stk is a stack whose every entry carries a running aggregate: the
// maximum of that entry's value and the values of all entries below it.
//
// The backing slice is written from bottom to top, so the last element is the
// stack top. stk depends on no other package.
package stk

// Entry is one stack element. Agg is the aggregate over Value and every value
// beneath it in the same stack; for the bottom entry Agg == Value.
type Entry struct {
	Value float64
	Agg   float64
}

// Stack is an aggregate stack. The zero value is NOT ready; use New.
type Stack struct {
	e []Entry
}

// New returns an empty stack.
func New() *Stack { return &Stack{} }

// Len is the number of entries.
func (s *Stack) Len() int { return len(s.e) }

// Push appends v to the top. Its aggregate is max(v, the previous top's
// aggregate); for the first entry the aggregate is v itself.
//
// Push is intended for finite values: NaN would poison every comparison. The
// api package rejects NaN before it can reach here.
func (s *Stack) Push(v float64) {
	a := v
	if n := len(s.e); n > 0 && s.e[n-1].Agg > v {
		a = s.e[n-1].Agg
	}
	s.e = append(s.e, Entry{Value: v, Agg: a})
}

// Pop removes and returns the top value. It returns false on an empty stack
// and leaves the stack unchanged.
func (s *Stack) Pop() (float64, bool) {
	n := len(s.e)
	if n == 0 {
		return 0, false
	}
	top := s.e[n-1]
	s.e = s.e[:n-1]
	return top.Value, true
}

// TopAgg returns the top entry's aggregate, or false when empty.
func (s *Stack) TopAgg() (float64, bool) {
	n := len(s.e)
	if n == 0 {
		return 0, false
	}
	return s.e[n-1].Agg, true
}

// Entries returns a copy of the contents bottom to top, for snapshots and
// invariant checks. Mutating the returned slice does not touch the stack.
func (s *Stack) Entries() []Entry {
	out := make([]Entry, len(s.e))
	copy(out, s.e)
	return out
}
