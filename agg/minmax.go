package agg

import "math"

// minAgg 仅持有当前最小值。被删值等于当前最小值时无法反推出新极值，
// 必须遍历成员重算，因此 NeedsMembers=true。
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

// Remove 返回 sensitive=true 当且仅当删到当前最小值。
func (a *minAgg) Remove(v float64) bool {
	if a.has && !(a.v < v) && !(v < a.v) {
		return true // v == a.v（含 +0/-0 视为相等的语义由 view 传值保证）
	}
	return false
}

func (a *minAgg) Recompute(values func(yield func(float64) bool)) {
	a.Reset()
	for v := range values {
		a.Add(v)
	}
}

// maxAgg 与 minAgg 对称。
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
	if a.has && !(a.v < v) && !(v < a.v) {
		return true
	}
	return false
}

func (a *maxAgg) Recompute(values func(yield func(float64) bool)) {
	a.Reset()
	for v := range values {
		a.Add(v)
	}
}
