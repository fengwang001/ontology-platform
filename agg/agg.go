// Package agg provides the aggregator family used by the grouped view.
package agg

import "math"

// Aggregator is one maintained aggregate over a group's values.
type Aggregator interface {
	Name() string
	// Insert/Delete update the aggregate for one member value.
	// Aggregators that cannot retract from scalar state return true from
	// NeedsMembersOnDelete and may leave Delete as a no-op: the view then
	// drives Recompute from the full member set.
	Insert(v float64)
	Delete(v float64)
	NeedsMembersOnDelete() bool
	// Recompute rebuilds state from zero or more current member values.
	Recompute(values []float64)
	// Result returns the scalar result and whether the group still exists.
	Result() (float64, bool)
}

func normZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// Count.
type Count struct{ n int }

func (a *Count) Name() string               { return "count" }
func (a *Count) Insert(float64)             { a.n++ }
func (a *Count) Delete(float64)             { a.n-- }
func (a *Count) NeedsMembersOnDelete() bool { return false }
func (a *Count) Recompute(vs []float64)     { a.n = len(vs) }
func (a *Count) Result() (float64, bool)    { return float64(a.n), a.n > 0 }

// Sum.
type Sum struct{ s float64 }

func (a *Sum) Name() string               { return "sum" }
func (a *Sum) Insert(v float64)           { a.s += v }
func (a *Sum) Delete(v float64)           { a.s -= v }
func (a *Sum) NeedsMembersOnDelete() bool { return false }
func (a *Sum) Recompute(vs []float64) {
	var s float64
	for _, v := range vs {
		s += v
	}
	a.s = s
}
func (a *Sum) Result() (float64, bool) { return a.s, true }

// Min.
type Min struct{ m float64 }

func (a *Min) Name() string               { return "min" }
func (a *Min) Insert(v float64)           { a.add(v) }
func (a *Min) Delete(float64)             {}
func (a *Min) NeedsMembersOnDelete() bool { return true }

func (a *Min) add(v float64) {
	v = normZero(v)
	if a.m == 0 || v < a.m {
		a.m = v
	}
}
func (a *Min) Recompute(vs []float64) {
	a.m = 0
	for _, v := range vs {
		a.add(v)
	}
}
func (a *Min) Result() (float64, bool) { return a.m, true }

// Max.
type Max struct {
	m   float64
	set bool
}

func (a *Max) Name() string               { return "max" }
func (a *Max) Insert(v float64)           { a.add(v) }
func (a *Max) Delete(float64)             {}
func (a *Max) NeedsMembersOnDelete() bool { return true }

func (a *Max) add(v float64) {
	v = normZero(v)
	if !a.set || v > a.m {
		a.m, a.set = v, true
	}
}
func (a *Max) Recompute(vs []float64) {
	a.m, a.set = 0, false
	for _, v := range vs {
		a.add(v)
	}
}
func (a *Max) Result() (float64, bool) { return a.m, true }

// DistinctCount counts distinct member values (+0/-0 equal).
type DistinctCount struct{ n int }

func (a *DistinctCount) Name() string               { return "distinct_count" }
func (a *DistinctCount) Insert(float64)             {}
func (a *DistinctCount) Delete(float64)             {}
func (a *DistinctCount) NeedsMembersOnDelete() bool { return true }
func (a *DistinctCount) Recompute(vs []float64) {
	seen := make(map[uint64]struct{}, len(vs))
	for _, v := range vs {
		seen[math.Float64bits(normZero(v))] = struct{}{}
	}
	a.n = len(seen)
}
func (a *DistinctCount) Result() (float64, bool) { return float64(a.n), true }

// New returns a fresh instance of a named aggregator.
func New(name string) Aggregator {
	switch name {
	case "count":
		return &Count{}
	case "sum":
		return &Sum{}
	case "min":
		return &Min{}
	case "max":
		return &Max{}
	case "distinct_count":
		return &DistinctCount{}
	}
	panic("agg: unknown aggregator " + name)
}

// DefaultNames is the standard family.
var DefaultNames = []string{"count", "sum", "min", "max", "distinct_count"}
