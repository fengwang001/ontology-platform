// Package agg defines the aggregator family used by the grouped view. Every
// aggregator explicitly declares whether deleting a value requires access to
// the group's member records (i.e. whether it supports incremental
// withdrawal).
package agg

import "math"

// Kind identifies an aggregator.
type Kind string

const (
	Count         Kind = "count"
	Sum           Kind = "sum"
	Min           Kind = "min"
	Max           Kind = "max"
	DistinctCount Kind = "distinct_count"
)

// State is one group's aggregate for one aggregator.
type State interface {
	Kind() Kind
	// Add folds an inserted member value into the state.
	Add(v float64)
	// Remove attempts incremental withdrawal. removed==true means the state
	// was updated; removed==false means the value governed the summary and
	// the caller must rebuild from members via Add calls on a fresh state.
	Remove(v float64) (removed bool)
	// Value returns the current summary (Count/Sum/DistinctCount numeric;
	// Min/Max return the extremum).
	Value() float64
	// NeedsMembersOnDelete declares the withdrawal strategy.
	NeedsMembersOnDelete() bool
}

type countState struct{ n float64 }

func (s *countState) Kind() Kind                  { return Count }
func (s *countState) Add(float64)                 { s.n++ }
func (s *countState) Remove(float64) bool         { s.n--; return true }
func (s *countState) Value() float64              { return s.n }
func (s *countState) NeedsMembersOnDelete() bool  { return false }

type sumState struct{ sum float64 }

func (s *sumState) Kind() Kind                 { return Sum }
func (s *sumState) Add(v float64)              { s.sum += norm(v) }
func (s *sumState) Remove(v float64) bool      { s.sum -= norm(v); if s.sum == 0 { s.sum = 0 }; return true }
func (s *sumState) Value() float64             { return s.sum }
func (s *sumState) NeedsMembersOnDelete() bool { return false }

type minState struct {
	min float64
	has bool
}

func (s *minState) Kind() Kind { return Min }
func (s *minState) Add(v float64) {
	v = norm(v)
	if !s.has || v < s.min {
		s.min, s.has = v, true
	}
}
func (s *minState) Remove(v float64) bool {
	if s.has && norm(v) == s.min {
		return false // runner-up is not derivable from the summary
	}
	return true
}
func (s *minState) Value() float64             { return s.min }
func (s *minState) NeedsMembersOnDelete() bool { return true }

type maxState struct {
	max float64
	has bool
}

func (s *maxState) Kind() Kind { return Max }
func (s *maxState) Add(v float64) {
	v = norm(v)
	if !s.has || v > s.max {
		s.max, s.has = v, true
	}
}
func (s *maxState) Remove(v float64) bool {
	if s.has && norm(v) == s.max {
		return false
	}
	return true
}
func (s *maxState) Value() float64             { return s.max }
func (s *maxState) NeedsMembersOnDelete() bool { return true }

// distinctState tracks values with multiplicities. The summary itself is the
// distinct set; the multiplicity map is cached member bookkeeping. Remove is
// still uncertain whenever the deleted value's last copy goes (the summary
// alone would not reveal it), so NeedsMembersOnDelete stays true and the view
// drives a Recompute in exactly that case.
type distinctState struct {
	set  map[uint64]uint64
}

func newDistinct() *distinctState { return &distinctState{set: map[uint64]uint64{}} }
func (s *distinctState) Kind() Kind                                 { return DistinctCount }
func (s *distinctState) Add(v float64)                              { s.set[math.Float64bits(norm(v))]++ }
func (s *distinctState) Remove(v float64) bool {
	b := math.Float64bits(norm(v))
	if s.set[b] <= 1 {
		return false // last copy: new distinct set requires the members
	}
	s.set[b]--
	return true
}
func (s *distinctState) Value() float64             { return float64(len(s.set)) }
func (s *distinctState) NeedsMembersOnDelete() bool { return true }

func norm(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// Equal reports bit-exact summary equality (NaN never reaches aggregators).
func Equal(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

// New returns a fresh zero-value state for the given kind.
func New(k Kind) State {
	switch k {
	case Count:
		return &countState{}
	case Sum:
		return &sumState{}
	case Min:
		return &minState{}
	case Max:
		return &maxState{}
	case DistinctCount:
		return newDistinct()
	default:
		panic("agg: unknown kind " + string(k))
	}
}

// AllKinds is the aggregator family in display order.
var AllKinds = []Kind{Count, Sum, Min, Max, DistinctCount}
