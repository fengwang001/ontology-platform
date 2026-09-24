// Package agg provides the aggregator family used by the incremental view.
// Each aggregator declares NeedsMembers: whether a deletion may require
// scanning the group's remaining members (a Recompute).
package agg

// Kind identifies an aggregator.
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

// AllKinds is the full aggregator family in canonical order.
var AllKinds = []Kind{Count, Sum, Min, Max, DistinctCount}

func (k Kind) String() string {
	return [...]string{"Count", "Sum", "Min", "Max", "DistinctCount"}[k]
}

// Aggregator maintains one aggregate value for a single group.
type Aggregator interface {
	Kind() Kind
	Add(v float64)
	Remove(v float64)
	NeedsMembers() bool
	NeedsRecompute(v float64) bool
	Recompute(members func(yield func(v float64) bool))
	Value() float64
	Clone() Aggregator
	Empty() bool
}

// New builds an empty aggregator of the given kind.
func New(k Kind) Aggregator {
	switch k {
	case Sum:
		return &sumAgg{}
	case Min:
		return &minAgg{}
	case Max:
		return &maxAgg{}
	case DistinctCount:
		return &distinctAgg{held: map[float64]int{}}
	default:
		return &countAgg{}
	}
}

// normZero collapses -0/+0 so the two zeroes compare equal.
func normZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

type countAgg struct{ n int }

func (a *countAgg) Kind() Kind                               { return Count }
func (a *countAgg) Add(float64)                              { a.n++ }
func (a *countAgg) Remove(float64)                           { a.n-- }
func (a *countAgg) NeedsMembers() bool                       { return false }
func (a *countAgg) NeedsRecompute(float64) bool              { return false }
func (a *countAgg) Recompute(func(yield func(float64) bool)) {}
func (a *countAgg) Value() float64                           { return float64(a.n) }
func (a *countAgg) Empty() bool                              { return a.n == 0 }
func (a *countAgg) Clone() Aggregator                        { return &countAgg{n: a.n} }

type sumAgg struct{ s float64 }

func (a *sumAgg) Kind() Kind                               { return Sum }
func (a *sumAgg) Add(v float64)                            { a.s += v }
func (a *sumAgg) Remove(v float64)                         { a.s -= v }
func (a *sumAgg) NeedsMembers() bool                       { return false }
func (a *sumAgg) NeedsRecompute(float64) bool              { return false }
func (a *sumAgg) Recompute(func(yield func(float64) bool)) {}
func (a *sumAgg) Value() float64                           { return a.s }
func (a *sumAgg) Empty() bool                              { return false }
func (a *sumAgg) Clone() Aggregator                        { return &sumAgg{s: a.s} }

type minAgg struct {
	n int
	m float64
}

func (a *minAgg) Kind() Kind { return Min }
func (a *minAgg) Add(v float64) {
	v = normZero(v)
	if a.n == 0 || v < a.m {
		a.m = v
	}
	a.n++
}
func (a *minAgg) Remove(v float64) {
	a.n--
	if a.n == 0 {
		a.m = 0
	}
}
func (a *minAgg) NeedsMembers() bool            { return true }
func (a *minAgg) NeedsRecompute(v float64) bool { return a.n > 0 && normZero(v) == a.m }
func (a *minAgg) Value() float64                { return a.m }
func (a *minAgg) Empty() bool                   { return a.n == 0 }
func (a *minAgg) Clone() Aggregator             { return &minAgg{n: a.n, m: a.m} }
func (a *minAgg) Recompute(members func(yield func(v float64) bool)) {
	a.n, a.m = 0, 0
	for v := range members {
		v = normZero(v)
		if a.n == 0 || v < a.m {
			a.m = v
		}
		a.n++
	}
}

type maxAgg struct {
	n int
	m float64
}

func (a *maxAgg) Kind() Kind { return Max }
func (a *maxAgg) Add(v float64) {
	v = normZero(v)
	if a.n == 0 || v > a.m {
		a.m = v
	}
	a.n++
}
func (a *maxAgg) Remove(v float64) {
	a.n--
	if a.n == 0 {
		a.m = 0
	}
}
func (a *maxAgg) NeedsMembers() bool            { return true }
func (a *maxAgg) NeedsRecompute(v float64) bool { return a.n > 0 && normZero(v) == a.m }
func (a *maxAgg) Value() float64                { return a.m }
func (a *maxAgg) Empty() bool                   { return a.n == 0 }
func (a *maxAgg) Clone() Aggregator             { return &maxAgg{n: a.n, m: a.m} }
func (a *maxAgg) Recompute(members func(yield func(v float64) bool)) {
	a.n, a.m = 0, 0
	for v := range members {
		v = normZero(v)
		if a.n == 0 || v > a.m {
			a.m = v
		}
		a.n++
	}
}

// distinctAgg counts distinct values via a per-value hold count.
type distinctAgg struct{ held map[float64]int }

func (a *distinctAgg) Kind() Kind    { return DistinctCount }
func (a *distinctAgg) Add(v float64) { a.held[normZero(v)]++ }
func (a *distinctAgg) Remove(v float64) {
	v = normZero(v)
	if a.held[v] <= 1 {
		delete(a.held, v)
	} else {
		a.held[v]--
	}
}
func (a *distinctAgg) NeedsMembers() bool { return true }
func (a *distinctAgg) NeedsRecompute(v float64) bool {
	return a.held[normZero(v)] == 1
}
func (a *distinctAgg) Value() float64 { return float64(len(a.held)) }
func (a *distinctAgg) Empty() bool    { return len(a.held) == 0 }
func (a *distinctAgg) Clone() Aggregator {
	c := &distinctAgg{held: make(map[float64]int, len(a.held))}
	for v, n := range a.held {
		c.held[v] = n
	}
	return c
}
func (a *distinctAgg) Recompute(members func(yield func(v float64) bool)) {
	a.held = map[float64]int{}
	for v := range members {
		a.held[normZero(v)]++
	}
}
