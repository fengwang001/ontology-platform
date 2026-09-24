package agg

import "math"

// countAgg：计数，删除贡献恒 -1，不需要成员。
type countAgg struct{ n int64 }

func (a *countAgg) Kind() Kind          { return Count }
func (a *countAgg) Add(float64)         { a.n++ }
func (a *countAgg) Remove(float64) bool { a.n--; return false }
func (a *countAgg) NeedsMembers() bool  { return false }
func (a *countAgg) Value() float64      { return float64(a.n) }
func (a *countAgg) Reset()              { a.n = 0 }
func (a *countAgg) Recompute(values func(func(float64) bool)) {
	var n int64
	for range values {
		n++
	}
	a.n = n
}

// sumAgg：总和，删除贡献恒 -v，不需要成员。
type sumAgg struct{ s float64 }

func (a *sumAgg) Kind() Kind            { return Sum }
func (a *sumAgg) Add(v float64)         { a.s += v }
func (a *sumAgg) Remove(v float64) bool { a.s -= v; return false }
func (a *sumAgg) NeedsMembers() bool    { return false }
func (a *sumAgg) Value() float64        { return a.s }
func (a *sumAgg) Reset()                { a.s = 0 }
func (a *sumAgg) Recompute(values func(func(float64) bool)) {
	var s float64
	for v := range values {
		s += v
	}
	a.s = s
}

// minAgg：仅持当前最小值；删到极值时需成员重算。
type minAgg struct {
	has bool
	v   float64
}

func (a *minAgg) Kind() Kind         { return Min }
func (a *minAgg) NeedsMembers() bool { return true }
func (a *minAgg) Reset()             { a.has, a.v = false, 0 }
func (a *minAgg) Value() float64 {
	if !a.has {
		return math.Inf(1)
	}
	return a.v
}
func (a *minAgg) Add(v float64) {
	if !a.has || v < a.v {
		a.v = v
	}
	a.has = true
}
func (a *minAgg) Remove(v float64) bool {
	return a.has && v == a.v
}
func (a *minAgg) Recompute(values func(func(float64) bool)) {
	a.Reset()
	for v := range values {
		a.Add(v)
	}
}

// maxAgg：与 min 对称。
type maxAgg struct {
	has bool
	v   float64
}

func (a *maxAgg) Kind() Kind         { return Max }
func (a *maxAgg) NeedsMembers() bool { return true }
func (a *maxAgg) Reset()             { a.has, a.v = false, 0 }
func (a *maxAgg) Value() float64 {
	if !a.has {
		return math.Inf(-1)
	}
	return a.v
}
func (a *maxAgg) Add(v float64) {
	if !a.has || v > a.v {
		a.v = v
	}
	a.has = true
}
func (a *maxAgg) Remove(v float64) bool {
	return a.has && v == a.v
}
func (a *maxAgg) Recompute(values func(func(float64) bool)) {
	a.Reset()
	for v := range values {
		a.Add(v)
	}
}

// distinctAgg：按位归并的不同值集合（+0/-0 归一），删除恒需成员。
type distinctAgg struct{ set map[uint64]struct{} }

func newDistinct() *distinctAgg     { return &distinctAgg{set: map[uint64]struct{}{}} }
func (a *distinctAgg) Kind() Kind   { return DistinctCount }
func (a *distinctAgg) NeedsMembers() bool { return true }
func (a *distinctAgg) Reset()       { a.set = map[uint64]struct{}{} }
func (a *distinctAgg) Value() float64 { return float64(len(a.set)) }

func distinctKey(v float64) uint64 {
	b := math.Float64bits(v)
	if b == 1<<63 {
		return 0
	}
	return b
}

func (a *distinctAgg) Add(v float64) { a.set[distinctKey(v)] = struct{}{} }
func (a *distinctAgg) Remove(float64) bool { return true }
func (a *distinctAgg) Recompute(values func(func(float64) bool)) {
	a.Reset()
	for v := range values {
		a.Add(v)
	}
}
