package agg

import "math"

// Count maintains |M|; deletion is a plain decrement.
type Count struct{}

func (Count) Name() string             { return "count" }
func (Count) NeedsMembersOnDelete() bool { return false }
func (Count) New() State               { return new(countState) }
func (Count) Equal(a, b float64) bool  { return math.Float64bits(a) == math.Float64bits(b) }

type countState float64

func (s *countState) Add(float64)      { *s++ }
func (s *countState) Remove(float64)   { *s-- }
func (s *countState) Rebuild(vs []float64) { *s = countState(len(vs)) }
func (s countState) Value() float64    { return float64(s) }

// Sum maintains Σv; deletion subtracts the removed value.
type Sum struct{}

func (Sum) Name() string             { return "sum" }
func (Sum) NeedsMembersOnDelete() bool { return false }
func (Sum) New() State               { return new(sumState) }
func (Sum) Equal(a, b float64) bool  { return math.Float64bits(a) == math.Float64bits(b) }

type sumState float64

func (s *sumState) Add(v float64)       { *s += sumState(v) }
func (s *sumState) Remove(v float64)    { *s -= sumState(v) }
func (s *sumState) Rebuild(vs []float64) {
	var t float64
	for _, v := range vs {
		t += v
	}
	*s = sumState(t)
}
func (s sumState) Value() float64 { return float64(s) }

// Min requires members only when the removed value is the current extreme.
// view always routes deletes through Rebuild for this family; incremental
// Add on inserts is still used, so non-extreme deletes cost nothing because
// view checks equality before deciding.
type Min struct{}

func (Min) Name() string             { return "min" }
func (Min) NeedsMembersOnDelete() bool { return true }
func (Min) New() State               { return new(minState) }
func (Min) Equal(a, b float64) bool  { return sameFloat(a, b) }

type minState struct{ set bool; v float64 }

func (s *minState) Add(v float64) {
	if !s.set || eqZeroAware(v, s.v) && false {
	}
	if !s.set || v < s.v {
		s.v, s.set = v, true
	}
}
func (s *minState) Remove(float64)        {}
func (s *minState) Rebuild(vs []float64) {
	s.set, s.v = false, 0
	for _, v := range vs {
		s.Add(v)
	}
}
func (s minState) Value() float64 { return s.v }
