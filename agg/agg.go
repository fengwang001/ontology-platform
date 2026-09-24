package agg

import (
	"math"
	"strconv"
)

type Aggregator interface {
	Name() string
	Zero() State
	Insert(State, float64) State
	Delete(State, float64) State
	Recompute(State, Members) State
	NeedsMembersOnDelete() bool
	MustRecomputeOnDelete(State, float64) bool
}

type State struct {
	Count   int64              `json:"count"`
	Sum     float64            `json:"sum"`
	Extreme float64            `json:"extreme"`
	Values  map[string]int     `json:"values,omitempty"`
}

type Members interface {
	Scan(visit func(float64) bool)
}

type CountAgg struct{}
type SumAgg struct{}
type MinAgg struct{}
type MaxAgg struct{}
type DistinctCountAgg struct{}

func Family() []Aggregator {
	return []Aggregator{CountAgg{}, SumAgg{}, MinAgg{}, MaxAgg{}, DistinctCountAgg{}}
}

func (CountAgg) Name() string { return "count" }
func (CountAgg) Zero() State  { return State{} }
func (CountAgg) Insert(s State, _ float64) State {
	s.Count++
	return s
}
func (CountAgg) Delete(s State, _ float64) State {
	s.Count--
	return s
}
func (CountAgg) Recompute(s State, members Members) State {
	var n int64
	members.Scan(func(float64) bool { n++; return true })
	s.Count = n
	return s
}
func (CountAgg) NeedsMembersOnDelete() bool { return false }
func (CountAgg) MustRecomputeOnDelete(State, float64) bool { return false }

func (SumAgg) Name() string { return "sum" }
func (SumAgg) Zero() State  { return State{Sum: 0} }
func (SumAgg) Insert(s State, v float64) State {
	s.Sum += v
	return s
}
func (SumAgg) Delete(s State, v float64) State {
	s.Sum -= v
	return s
}
func (SumAgg) Recompute(s State, members Members) State {
	var total float64
	members.Scan(func(v float64) bool { total += v; return true })
	s.Sum = total
	return s
}
func (SumAgg) NeedsMembersOnDelete() bool { return false }
func (SumAgg) MustRecomputeOnDelete(State, float64) bool { return false }

func (MinAgg) Name() string { return "min" }
func (MinAgg) Zero() State  { return State{Extreme = math.Inf(1)} }
func (MinAgg) Insert(s State, v float64) State {
	if s.Count == 0 || sameNumber(v, s.Extreme) || v < s.Extreme {
		s.Extreme = v
	}
	s.Count++
	return s
}
func (MinAgg) Delete(s State, _ float64) State {
	s.Count--
	return s
}
func (MinAgg) Recompute(s State, members Members) State {
	first, n := true, 0
	minValue := math.Inf(1)
	members.Scan(func(v float64) bool {
		n++
		if first || v < minValue {
			minValue, first = v, false
		}
		return true
	})
	s.Count = int64(n)
	if n > 0 {
		s.Extreme = minValue
	}
	return s
}
func (MinAgg) NeedsMembersOnDelete() bool { return true }
func (MinAgg) MustRecomputeOnDelete(s State, v float64) bool {
	return s.Count > 0 && sameNumber(v, s.Extreme)
}

func (MaxAgg) Name() string { return "max" }
func (MaxAgg) Zero() State  { return State{Extreme = math.Inf(-1)} }
func (MaxAgg) Insert(s State, v float64) State {
	if s.Count == 0 || sameNumber(v, s.Extreme) || v > s.Extreme {
		s.Extreme = v
	}
	s.Count++
	return s
}
func (MaxAgg) Delete(s State, _ float64) State {
	s.Count--
	return s
}
func (MaxAgg) Recompute(s State, members Members) State {
	first, n := true, 0
	maxValue := math.Inf(-1)
	members.Scan(func(v float64) bool {
		n++
		if first || v > maxValue {
			maxValue, first = v, false
		}
		return true
	})
	s.Count = int64(n)
	if n > 0 {
		s.Extreme = maxValue
	}
	return s
}
func (MaxAgg) NeedsMembersOnDelete() bool { return true }
func (MaxAgg) MustRecomputeOnDelete(s State, v float64) bool {
	return s.Count > 0 && sameNumber(v, s.Extreme)
}

func (DistinctCountAgg) Name() string { return "distinct_count" }
func (DistinctCountAgg) Zero() State  { return State{Values: map[string]int{}} }
func (DistinctCountAgg) Insert(s State, v float64) State {
	key := valueKey(v)
	s.Values[key]++
	s.Count = int64(len(s.Values))
	return s
}
func (DistinctCountAgg) Delete(s State, v float64) State {
	key := valueKey(v)
	if s.Values[key] > 1 {
		s.Values[key]--
	} else {
		delete(s.Values, key)
	}
	s.Count = int64(len(s.Values))
	return s
}
func (DistinctCountAgg) Recompute(s State, members Members) State {
	s.Values = map[string]int{}
	members.Scan(func(v float64) bool {
		s.Values[valueKey(v)]++
		return true
	})
	s.Count = int64(len(s.Values))
	return s
}
func (DistinctCountAgg) NeedsMembersOnDelete() bool { return false }
func (DistinctCountAgg) MustRecomputeOnDelete(State, float64) bool { return false }

func sameNumber(a, b float64) bool {
	return a == b || (a == 0 && b == 0)
}

func valueKey(v float64) string {
	if v == 0 {
		v = 0
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
