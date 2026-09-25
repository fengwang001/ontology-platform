// Package agg 提供可增量维护的分组聚合器族。
//
// 撤回语义：Add 处理插入；Remove 处理删除，返回 true 表示"仅凭聚合状态无法
// 完成撤回，必须拿到该组当前成员重算"。Count/Sum 恒返回 false；Min/Max 仅
// 在删到当前极值时返回 true；DistinctCount 每次删除都返回 true。
package agg

import "math"

// Aggregator 是单个分组上的单个聚合。
type Aggregator interface {
	Add(v float64)
	Remove(v float64) (needRecompute bool)
	Rebuild(members []float64)
	Value() float64
}

// Kind 标识聚合器种类。
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

// New 按种类构造聚合器。
func New(k Kind) Aggregator {
	switch k {
	case Count:
		return &countAgg{}
	case Sum:
		return &sumAgg{}
	case Min:
		return &minAgg{init: true}
	case Max:
		return &maxAgg{init: true}
	case DistinctCount:
		return &distinctAgg{set: map[uint64]struct{}{}}
	default:
		panic("agg: unknown kind")
	}
}

// NeedsMembersOnDelete 显式声明该聚合器删除时是否"可能需要该组成员"。
// Min/Max 是条件需要（删到极值时），DistinctCount 恒需要；Count/Sum 不需要。
func NeedsMembersOnDelete(k Kind) bool {
	return k == Min || k == Max || k == DistinctCount
}

type countAgg struct{ n int }

func (a *countAgg) Add(float64)        { a.n++ }
func (a *countAgg) Remove(float64) bool { a.n--; return false }
func (a *countAgg) Rebuild(m []float64) { a.n = len(m) }
func (a *countAgg) Value() float64      { return float64(a.n) }

type sumAgg struct{ s float64 }

func (a *sumAgg) Add(v float64)          { a.s += v }
func (a *sumAgg) Remove(v float64) bool  { a.s -= v; return false }
func (a *sumAgg) Rebuild(m []float64) {
	a.s = 0
	for _, v := range m {
		a.s += v
	}
}
func (a *sumAgg) Value() float64 { return a.s }

type minAgg struct {
	v    float64
	init bool
}

func (a *minAgg) Add(v float64) {
	if a.init || v < a.v {
		a.v, a.init = v, false
	}
}
func (a *minAgg) Remove(v float64) bool { return !a.init && v == a.v }
func (a *minAgg) Rebuild(m []float64) {
	a.init = true
	for _, v := range m {
		a.Add(v)
	}
}
func (a *minAgg) Value() float64 { return a.v }

type maxAgg struct {
	v    float64
	init bool
}

func (a *maxAgg) Add(v float64) {
	if a.init || v > a.v {
		a.v, a.init = v, false
	}
}
func (a *maxAgg) Remove(v float64) bool { return !a.init && v == a.v }
func (a *maxAgg) Rebuild(m []float64) {
	a.init = true
	for _, v := range m {
		a.Add(v)
	}
}
func (a *maxAgg) Value() float64 { return a.v }

// distinctAgg 保存去重值集合（按 IEEE754 位归一，±0 位不同但数值相等，
// 故零单独记 canonical zero）。
type distinctAgg struct {
	set  map[uint64]struct{}
	zero bool
}

func (a *distinctAgg) Add(v float64) {
	if v == 0 {
		a.zero = true
		return
	}
	a.set[math.Float64bits(v)] = struct{}{}
}
func (a *distinctAgg) Remove(float64) bool { return true }
func (a *distinctAgg) Rebuild(m []float64) {
	a.set = map[uint64]struct{}{}
	a.zero = false
	for _, v := range m {
		a.Add(v)
	}
}
func (a *distinctAgg) Value() float64 {
	n := len(a.set)
	if a.zero {
		n++
	}
	return float64(n)
}
