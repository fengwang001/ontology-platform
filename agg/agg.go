// Package agg defines the family of group aggregators and whether each one
// can retract a deletion incrementally or must be recomputed from members.
package agg

import (
	"math"

	"ontology/change"
)

// Kind identifies a built-in aggregator.
type Kind string

const (
	Count         Kind = "count"
	Sum           Kind = "sum"
	Min           Kind = "min"
	Max           Kind = "max"
	DistinctCount Kind = "distinct_count"
)

// Kinds lists all built-in aggregators in a stable order.
func Kinds() []Kind {
	return []Kind{Count, Sum, Min, Max, DistinctCount}
}

// Aggregator maintains one aggregate value for one group.
//
// Insert/Delete update state incrementally. Delete returns false when the
// aggregate cannot be maintained from its state alone for this particular
// deletion; the view must then call Recompute with the surviving members.
// Present reports whether the aggregate holds any records (empty groups vanish).
type Aggregator interface {
	Kind() Kind
	Insert(v float64)
	Delete(v float64) bool
	Recompute(members []float64)
	Value() (float64, bool)
	Copy() Aggregator
}

func norm(v float64) float64 {
	// +0 and -0 compare equal everywhere here; canonicalise the map key/state.
	if v == 0 {
		return 0
	}
	return v
}

type countAgg struct{ n int64 }

func newCount() Aggregator         { return &countAgg{} }
func (a *countAgg) Kind() Kind     { return Count }
func (a *countAgg) Insert(float64) { a.n++ }
func (a *countAgg) Delete(float64) bool {
	a.n--
	return true
}
func (a *countAgg) Recompute(m []float64) { a.n = int64(len(m)) }
func (a *countAgg) Value() (float64, bool) {
	return float64(a.n), a.n > 0
}
func (a *countAgg) Copy() Aggregator { c := *a; return &c }

type sumAgg struct{ s float64 }

func newSum() Aggregator           { return &sumAgg{} }
func (a *sumAgg) Kind() Kind       { return Sum }
func (a *sumAgg) Insert(v float64) { a.s += norm(v) }
func (a *sumAgg) Delete(v float64) bool {
	a.s -= norm(v)
	return true
}
func (a *sumAgg) Recompute(m []float64) {
	var s float64
	for _, v := range m {
		s += norm(v)
	}
	a.s = s
}
func (a *sumAgg) Value() (float64, bool) { return a.s, true }
func (a *sumAgg) Copy() Aggregator       { c := *a; return &c }

type extrema struct {
	kind Kind
	ok   bool
	v    float64
}

func newMin() Aggregator { return &extrema{kind: Min} }
func newMax() Aggregator { return &extrema{kind: Max} }

func (a *extrema) Kind() Kind { return a.kind }

func (a *extrema) better(v, cur float64) bool {
	if a.kind == Min {
		return v < cur
	}
	return v > cur
}

func (a *extrema) Insert(v float64) {
	v = norm(v)
	if !a.ok || a.better(v, a.v) {
		a.v, a.ok = v, true
	}
}

func (a *extrema) Delete(v float64) bool {
	v = norm(v)
	// Only deleting the current extremum forces a recompute; ties leave it.
	if a.ok && v == a.v {
		return false
	}
	return true
}

func (a *extrema) Recompute(m []float64) {
	a.ok = false
	var v float64
	for _, x := range m {
		x = norm(x)
		if !a.ok || a.better(x, v) {
			v, a.ok = x, true
		}
	}
	a.v = v
}

func (a *extrema) Value() (float64, bool) { return a.v, a.ok }
func (a *extrema) Copy() Aggregator       { c := *a; return &c }

type distinct struct{ hold map[float64]int }

func newDistinct() Aggregator  { return &distinct{hold: map[float64]int{}} }
func (a *distinct) Kind() Kind { return DistinctCount }

func (a *distinct) Insert(v float64) {
	v = norm(v)
	a.hold[v]++
}

// Delete always returns false: deciding whether the deleted value is still
// held by another record requires membership information, so the view
// recomputes this aggregate from the surviving members.
func (a *distinct) Delete(float64) bool { return false }

func (a *distinct) Recompute(m []float64) {
	a.hold = make(map[float64]int, len(m))
	for _, v := range m {
		a.hold[norm(v)]++
	}
}

func (a *distinct) Value() (float64, bool) {
	return float64(len(a.hold)), len(a.hold) > 0
}

func (a *distinct) Copy() Aggregator {
	c := &distinct{hold: make(map[float64]int, len(a.hold))}
	for k, v := range a.hold {
		c.hold[k] = v
	}
	return c
}

// New builds an aggregator of the given kind.
func New(k Kind) Aggregator {
	switch k {
	case Count:
		return newCount()
	case Sum:
		return newSum()
	case Min:
		return newMin()
	case Max:
		return newMax()
	case DistinctCount:
		return newDistinct()
	default:
		return nil
	}
}

// NeedsMembers reports whether applying op to k needs the group's members
// (i.e. cannot be performed from aggregate state alone).
func NeedsMembers(k Kind, op change.Op) bool {
	if op != change.Delete {
		return false
	}
	return k == Min || k == Max || k == DistinctCount
}

// FloatBits helps compare values at IEEE754 bit precision.
func FloatBits(v float64) uint64 { return math.Float64bits(v) }
