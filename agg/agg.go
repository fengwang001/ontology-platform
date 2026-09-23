// Package agg defines the aggregator family used by the incremental view.
//
// Each aggregator declares whether deleting a record requires access to the
// surviving members of the group (NeedsMembersOnDelete). When it does, the
// view triggers a Recompute pass instead of trusting an in-place withdrawal.
package agg

import "math"

// Aggregator maintains one aggregate value over a single group's members.
//
// Lifecycle of a delete:
//   - Delete(v) applies the in-place withdrawal if it is safe. It returns
//     true when the aggregate can no longer be maintained incrementally and
//     the view must call Recompute over the surviving members.
//   - Recompute resets and rebuilds the state; the view feeds each surviving
//     member through Insert.
type Aggregator interface {
	Name() string
	Insert(v float64)
	Delete(v float64) (needsRecompute bool)
	Reset()
	Value() float64
	// NeedsMembersOnDelete reports the aggregator's withdrawal policy.
	NeedsMembersOnDelete() bool
}

// Count counts records; deletion is always a plain decrement.
type Count struct{ n int64 }

func NewCount() *Count               { return &Count{} }
func (a *Count) Name() string         { return "count" }
func (a *Count) Insert(float64)       { a.n++ }
func (a *Count) Delete(float64) bool  { a.n--; return false }
func (a *Count) Reset()               { a.n = 0 }
func (a *Count) Value() float64       { return float64(a.n) }
func (a *Count) NeedsMembersOnDelete() bool { return false }

// Sum sums values; deletion subtracts the known removed value.
type Sum struct{ s float64 }

func NewSum() *Sum                  { return &Sum{} }
func (a *Sum) Name() string          { return "sum" }
func (a *Sum) Insert(v float64)      { a.s += v }
func (a *Sum) Delete(v float64) bool { a.s -= v; return false }
func (a *Sum) Reset()                { a.s = 0 }
func (a *Sum) Value() float64        { return a.s }
func (a *Sum) NeedsMembersOnDelete() bool { return false }

// Min keeps only the current minimum. Deletion is safe unless the removed
// value is the current extremum; then the runner-up is unknown.
type Min struct{ v float64 }

func NewMin() *Min  { return &Min{v: math.Inf(1)} }
func (a *Min) Name() string { return "min" }
func (a *Min) Insert(v float64) {
	if a.empty() || v < a.v {
		a.v = v
	}
}
func (a *Min) Delete(v float64) bool { return v == a.v }
func (a *Min) Reset()                { a.v = math.Inf(1) }
func (a *Min) Value() float64        { return a.v }
func (a *Min) NeedsMembersOnDelete() bool { return true }
func (a *Min) empty() bool           { return math.IsInf(a.v, 1) }

// Max keeps only the current maximum; mirror of Min.
type Max struct{ v float64 }

func NewMax() *Max  { return &Max{v: math.Inf(-1)} }
func (a *Max) Name() string { return "max" }
func (a *Max) Insert(v float64) {
	if a.empty() || v > a.v {
		a.v = v
	}
}
func (a *Max) Delete(v float64) bool { return v == a.v }
func (a *Max) Reset()                { a.v = math.Inf(-1) }
func (a *Max) Value() float64        { return a.v }
func (a *Max) NeedsMembersOnDelete() bool { return true }
func (a *Max) empty() bool           { return math.IsInf(a.v, -1) }

// DistinctCount counts distinct values. The single output number cannot tell
// whether another surviving member still holds the removed value, so every
// deletion requires members; the view rebuilds via Insert (duplicate inserts
// are idempotent for counting purposes because the view feeds the multiset).
type DistinctCount struct {
	seen map[float64]struct{}
}

func NewDistinctCount() *DistinctCount {
	return &DistinctCount{seen: map[float64]struct{}{}}
}
func (a *DistinctCount) Name() string   { return "distinct_count" }
func (a *DistinctCount) Insert(v float64) { a.seen[v] = struct{}{} }
func (a *DistinctCount) Delete(float64) bool { return true }
func (a *DistinctCount) Reset() {
	a.seen = map[float64]struct{}{}
}
func (a *DistinctCount) Value() float64        { return float64(len(a.seen)) }
func (a *DistinctCount) NeedsMembersOnDelete() bool { return true }

// Registry returns a fresh ordered set of the built-in aggregators.
func Registry() []Aggregator {
	return []Aggregator{
		NewCount(),
		NewSum(),
		NewMin(),
		NewMax(),
		NewDistinctCount(),
	}
}
