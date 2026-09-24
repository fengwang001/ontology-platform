package agg

import "math"

// distinctAgg 维护组内不同值的集合（按 Float64bits 归并，
// 使 +0/-0 视为同一值）。聚合状态无法判断被删值是否仍有其他记录持有，
// 故删除时总是需要成员重算。
type distinctAgg struct{ set map[uint64]struct{} }

func newDistinct() *distinctAgg { return &distinctAgg{set: map[uint64]struct{}{}} }

func (a *distinctAgg) Kind() Kind         { return DistinctCount }
func (a *distinctAgg) NeedsMembers() bool { return true }
func (a *distinctAgg) Reset()             { a.set = map[uint64]struct{}{} }
func (a *distinctAgg) Value() float64     { return float64(len(a.set)) }

func key(v float64) uint64 {
	b := math.Float64bits(v)
	if b == 1<<63 { // -0.0 → +0.0
		return 0
	}
	return b
}

func (a *distinctAgg) Add(v float64) { a.set[key(v)] = struct{}{} }

// Remove 无法仅凭集合判断该值是否仍有持有者，恒返回 sensitive。
func (a *distinctAgg) Remove(float64) bool { return true }

func (a *distinctAgg) Recompute(values func(yield func(float64) bool)) {
	a.Reset()
	for v := range values {
		a.Add(v)
	}
}
