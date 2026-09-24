package agg

// countAgg 维护成员计数。删除贡献恒为 -1，永远不需要重算成员。
type countAgg struct{ n int64 }

func (a *countAgg) Kind() Kind          { return Count }
func (a *countAgg) Add(float64)         { a.n++ }
func (a *countAgg) Remove(float64) bool { a.n--; return false }
func (a *countAgg) NeedsMembers() bool  { return false }
func (a *countAgg) Value() float64      { return float64(a.n) }
func (a *countAgg) Reset()              { a.n = 0 }

func (a *countAgg) Recompute(values func(yield func(float64) bool)) {
	var n int64
	for range values {
		n++
	}
	a.n = n
}

// sumAgg 维护值总和。删除贡献恒为 -v，不需要重算成员。
type sumAgg struct{ s float64 }

func (a *sumAgg) Kind() Kind    { return Sum }
func (a *sumAgg) Add(v float64) { a.s += v }
func (a *sumAgg) Remove(v float64) bool {
	a.s -= v
	return false
}
func (a *sumAgg) NeedsMembers() bool { return false }
func (a *sumAgg) Value() float64     { return a.s }
func (a *sumAgg) Reset()             { a.s = 0 }

func (a *sumAgg) Recompute(values func(yield func(float64) bool)) {
	var s float64
	for v := range values {
		s += v
	}
	a.s = s
}
