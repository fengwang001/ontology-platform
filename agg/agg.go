package agg

import "math"

type Kind string

const (
	Count         Kind = "count"
	Sum           Kind = "sum"
	Min           Kind = "min"
	Max           Kind = "max"
	DistinctCount Kind = "distinct_count"
)

type Member struct {
	ID    string
	Value float64
}

type Aggregator interface {
	Kind() Kind
	NeedsMembers() bool
	Insert(float64)
	Delete(float64) bool
	Recompute([]Member)
	Snapshot() (float64, bool)
}

func norm(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

type countAgg struct{ n int }

func (a *countAgg) Kind() Kind                { return Count }
func (a *countAgg) NeedsMembers() bool        { return false }
func (a *countAgg) Insert(float64)            { a.n++ }
func (a *countAgg) Delete(float64) bool       { a.n--; return false }
func (a *countAgg) Recompute(ms []Member)     { a.n = len(ms) }
func (a *countAgg) Snapshot() (float64, bool) { return float64(a.n), a.n > 0 }

type sumAgg struct{ sum float64 }

func (a *sumAgg) Kind() Kind            { return Sum }
func (a *sumAgg) NeedsMembers() bool    { return false }
func (a *sumAgg) Insert(v float64)      { a.sum += norm(v) }
func (a *sumAgg) Delete(v float64) bool { a.sum -= norm(v); return false }
func (a *sumAgg) Recompute(ms []Member) {
	a.sum = 0
	for _, m := range ms {
		a.sum += norm(m.Value)
	}
}
func (a *sumAgg) Snapshot() (float64, bool) { return a.sum, true }

type minAgg struct{ v float64 }

func newMin() *minAgg { return &minAgg{v: math.NaN()} }

func (a *minAgg) Kind() Kind         { return Min }
func (a *minAgg) NeedsMembers() bool { return true }
func (a *minAgg) Insert(v float64) {
	v = norm(v)
	if math.IsNaN(a.v) || v < a.v {
		a.v = v
	}
}
func (a *minAgg) Delete(v float64) bool { return norm(v) == a.v }
func (a *minAgg) Recompute(ms []Member) {
	if len(ms) == 0 {
		a.v = math.NaN()
		return
	}
	a.v = norm(ms[0].Value)
	for _, m := range ms[1:] {
		if v := norm(m.Value); v < a.v {
			a.v = v
		}
	}
}
func (a *minAgg) Snapshot() (float64, bool) { return a.v, !math.IsNaN(a.v) }

type maxAgg struct{ v float64 }

func newMax() *maxAgg { return &maxAgg{v: math.NaN()} }

func (a *maxAgg) Kind() Kind         { return Max }
func (a *maxAgg) NeedsMembers() bool { return true }
func (a *maxAgg) Insert(v float64) {
	v = norm(v)
	if math.IsNaN(a.v) || v > a.v {
		a.v = v
	}
}
func (a *maxAgg) Delete(v float64) bool { return norm(v) == a.v }
func (a *maxAgg) Recompute(ms []Member) {
	if len(ms) == 0 {
		a.v = math.NaN()
		return
	}
	a.v = norm(ms[0].Value)
	for _, m := range ms[1:] {
		if v := norm(m.Value); v > a.v {
			a.v = v
		}
	}
}
func (a *maxAgg) Snapshot() (float64, bool) { return a.v, !math.IsNaN(a.v) }

type distinctAgg struct{ values map[float64]int }

func (a *distinctAgg) Kind() Kind         { return DistinctCount }
func (a *distinctAgg) NeedsMembers() bool { return true }
func (a *distinctAgg) Insert(v float64) {
	v = norm(v)
	a.values[v]++
}
func (a *distinctAgg) Delete(float64) bool { return true }
func (a *distinctAgg) Recompute(ms []Member) {
	clear(a.values)
	for _, m := range ms {
		a.values[norm(m.Value)]++
	}
}
func (a *distinctAgg) Snapshot() (float64, bool) { return float64(len(a.values)), len(a.values) > 0 }

func New(kind Kind) Aggregator {
	switch kind {
	case Count:
		return &countAgg{}
	case Sum:
		return &sumAgg{}
	case Min:
		return newMin()
	case Max:
		return newMax()
	default:
		return &distinctAgg{values: map[float64]int{}}
	}
}

func All() []Kind { return []Kind{Count, Sum, Min, Max, DistinctCount} }
