// Package agg provides the aggregator family and their withdraw policy.
package agg

import "math"

// Kind identifies one aggregator.
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
	NumKinds
)

// Aggregator is maintained incrementally for one group.
type Aggregator interface {
	// Insert adds a member value.
	Insert(v float64)
	// Delete withdraws a member value; needMembers reports whether the
	// aggregate must instead be rebuilt from the remaining group members.
	Delete(v float64) (needMembers bool)
	// Reset drops all state; rebuild calls Insert for every surviving member.
	Reset()
	// Value returns the aggregate; valid only while the group is non-empty.
	Value() float64
}

type countAgg struct{ n int64 }

func (a *countAgg) Insert(float64)      { a.n++ }
func (a *countAgg) Delete(float64) bool { a.n--; return false }
func (a *countAgg) Reset()              { a.n = 0 }
func (a *countAgg) Value() float64      { return float64(a.n) }

type sumAgg struct{ s float64 }

func (a *sumAgg) Insert(v float64)      { a.s += v }
func (a *sumAgg) Delete(v float64) bool { a.s -= v; return false }
func (a *sumAgg) Reset()                { a.s = 0 }
func (a *sumAgg) Value() float64        { return a.s }

// Min: a withdraw only needs members when the removed value is the extremum.
type minAgg struct{ m float64 }

func (a *minAgg) Insert(v float64) {
	if math.IsNaN(a.m) || norm(v) < norm(a.m) {
		a.m = v
	}
}
func (a *minAgg) Delete(v float64) bool { return eqVal(v, a.m) }
func (a *minAgg) Reset()                { a.m = math.NaN() }
func (a *minAgg) Value() float64        { return a.m }

type maxAgg struct{ m float64 }

func (a *maxAgg) Insert(v float64) {
	if math.IsNaN(a.m) || norm(v) > norm(a.m) {
		a.m = v
	}
}
func (a *maxAgg) Delete(v float64) bool { return eqVal(v, a.m) }
func (a *maxAgg) Reset()                { a.m = math.NaN() }
func (a *maxAgg) Value() float64        { return a.m }

// distinctAgg keeps a per-value holder count, so duplicate holders are known.
type distinctAgg struct {
	h map[uint64]int
}

func (a *distinctAgg) Insert(v float64) {
	k := math.Float64bits(norm(v))
	a.h[k]++
}
func (a *distinctAgg) Delete(float64) bool { return true }
func (a *distinctAgg) Reset()              { a.h = map[uint64]int{} }
func (a *distinctAgg) Value() float64      { return float64(len(a.h)) }

// NeedsMembersOnDelete reports whether a delete of value v in this group must
// trigger the Recompute phase (instead of a purely incremental withdraw).
func NeedsMembersOnDelete(k Kind, v, current float64) bool {
	switch k {
	case Min:
		return eqVal(v, current)
	case Max:
		return eqVal(v, current)
	case DistinctCount:
		return true
	default:
		return false
	}
}

// New builds one fresh aggregator of kind k.
func New(k Kind) Aggregator {
	switch k {
	case Count:
		return &countAgg{}
	case Sum:
		return &sumAgg{}
	case Min:
		return &minAgg{m: math.NaN()}
	case Max:
		return &maxAgg{m: math.NaN()}
	default:
		return &distinctAgg{h: map[uint64]int{}}
	}
}

// norm treats +0 and -0 as the same value.
func norm(v float64) float64 {
	if eqZero(v) {
		return 0
	}
	return v
}

func eqZero(v float64) bool { return math.Float64bits(v) == 0 || math.Float64bits(v) == 1<<63 }

func eqVal(x, y float64) bool { return norm(x) == norm(y) }
