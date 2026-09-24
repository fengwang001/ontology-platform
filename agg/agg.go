// Package agg defines incremental aggregators and their withdraw policy.
package agg

// Kind identifies a concrete aggregator.
type Kind uint8

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

// Aggregator maintains one group's scalar aggregate.
//
// Remove is an incremental withdraw. It returns needRecompute=true when the
// new value cannot be derived from the scalar state alone; the view then
// calls Recompute with the group's surviving members.
type Aggregator interface {
	Kind() Kind
	Add(v float64)
	Remove(v float64) (needRecompute bool)
	Recompute(members []float64)
	Value() float64
	NeedsMembersOnDelete() bool
	Reset()
}

// New builds an aggregator of the requested kind.
func New(k Kind) Aggregator {
	switch k {
	case Count:
		return &countA{}
	case Sum:
		return &sumA{}
	case Min:
		return &minA{set: false}
	case Max:
		return &maxA{set: false}
	case DistinctCount:
		return &distinctA{m: map[float64]int{}}
	default:
		panic("agg: unknown kind")
	}
}

// AllKinds lists every shipped aggregator.
func AllKinds() []Kind { return []Kind{Count, Sum, Min, Max, DistinctCount} }

// SameValue treats +0 and -0 as equal; otherwise exact float equality.
func SameValue(a, b float64) bool {
	if a == 0 && b == 0 {
		return true
	}
	return a == b
}

type countA struct{ n int }

func (a *countA) Kind() Kind                         { return Count }
func (a *countA) Add(float64)                        { a.n++ }
func (a *countA) Remove(float64) bool                { a.n--; return false }
func (a *countA) Recompute(m []float64)              { a.n = len(m) }
func (a *countA) Value() float64                     { return float64(a.n) }
func (a *countA) NeedsMembersOnDelete() bool         { return false }
func (a *countA) Reset()                             { a.n = 0 }

type sumA struct{ s float64 }

func (a *sumA) Kind() Kind  { return Sum }
func (a *sumA) Add(v float64) { a.s += v }
func (a *sumA) Remove(v float64) bool {
	a.s -= v
	return false
}
func (a *sumA) Recompute(m []float64) {
	var s float64
	for _, v := range m {
		s += v
	}
	a.s = s
}
func (a *sumA) Value() float64             { return a.s }
func (a *sumA) NeedsMembersOnDelete() bool { return false }
func (a *sumA) Reset()                     { a.s = 0 }

type minA struct {
	v   float64
	set bool
}

func (a *minA) Kind() Kind { return Min }
func (a *minA) Add(v float64) {
	if !a.set || v < a.v {
		a.v, a.set = v, true
	}
}
func (a *minA) Remove(v float64) bool { return SameValue(v, a.v) }
func (a *minA) Recompute(m []float64) {
	a.set = false
	for _, v := range m {
		a.Add(v)
	}
}
func (a *minA) Value() float64             { return a.v }
func (a *minA) NeedsMembersOnDelete() bool { return true }
func (a *minA) Reset()                     { a.v, a.set = 0, false }

type maxA struct {
	v   float64
	set bool
}

func (a *maxA) Kind() Kind { return Max }
func (a *maxA) Add(v float64) {
	if !a.set || v > a.v {
		a.v, a.set = v, true
	}
}
func (a *maxA) Remove(v float64) bool { return SameValue(v, a.v) }
func (a *maxA) Recompute(m []float64) {
	a.set = false
	for _, v := range m {
		a.Add(v)
	}
}
func (a *maxA) Value() float64             { return a.v }
func (a *maxA) NeedsMembersOnDelete() bool { return true }
func (a *maxA) Reset()                     { a.v, a.set = 0, false }

// distinctA intentionally keeps only the scalar answer: deletes cannot tell
// whether another record still holds the value, so every delete recomputes.
type distinctA struct {
	n int
	m map[float64]int // retained solely so inserts stay incremental
}

func (a *distinctA) Kind() Kind { return DistinctCount }
func (a *distinctA) Add(v float64) {
	key := normalizeZero(v)
	if a.m[key] == 0 {
		a.n++
	}
	a.m[key]++
}
func (a *distinctA) Remove(float64) bool { return true }
func (a *distinctA) Recompute(m []float64) {
	a.m = map[float64]int{}
	a.n = 0
	for _, v := range m {
		key := normalizeZero(v)
		if a.m[key] == 0 {
			a.n++
		}
		a.m[key]++
	}
}
func (a *distinctA) Value() float64             { return float64(a.n) }
func (a *distinctA) NeedsMembersOnDelete() bool { return true }
func (a *distinctA) Reset()                     { a.m = map[float64]int{}; a.n = 0 }

// normalizeZero maps -0 to +0 so the two zeros count as one distinct value.
func normalizeZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}
