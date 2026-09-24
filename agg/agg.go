// Package agg 提供 Count、Sum、Min、Max、DistinctCount 聚合器族。
package agg

import "math"

// Kind 标识一种聚合器。
type Kind int

const (
	Count Kind = iota
	Sum
	Min
	Max
	DistinctCount
)

// Aggregator 是单个聚合在单个分组上的状态。
type Aggregator interface {
	Kind() Kind
	// Add 并入一条值为 v 的成员（已规范化）。
	Add(v float64)
	// Remove 撤回一条成员；返回 true 表示调用方必须用成员 Rebuild。
	Remove(v float64) (needsRebuild bool)
	// Rebuild 用该组当前全部成员重建状态。
	Rebuild(memberValues []float64)
	// Result 返回聚合值；ok=false 表示空组上该聚合无结果。
	Result() (r float64, ok bool)
	// NeedsMembersOnDelete 显式声明删除时是否需要该组成员。
	NeedsMembersOnDelete() bool
}

// New 按类型创建空聚合器。
func New(k Kind) Aggregator {
	switch k {
	case Count:
		return &countA{}
	case Sum:
		return &sumA{}
	case Min:
		return &minMaxA{min: true}
	case Max:
		return &minMaxA{min: false}
	case DistinctCount:
		return &distinctA{m: map[uint64]struct{}{}}
	default:
		panic("agg: unknown kind")
	}
}

// Norm 统一 +0/-0 的位模式（NaN 由上层拒绝，不会进入）。
func Norm(v float64) float64 {
	if bits := math.Float64bits(v); bits == 0x8000000000000000 {
		return 0
	}
	return v
}

type countA struct{ n int }

func (a *countA) Kind() Kind                  { return Count }
func (a *countA) Add(float64)                 { a.n++ }
func (a *countA) Remove(float64) bool         { a.n--; return false }
func (a *countA) Rebuild(vs []float64)        { a.n = len(vs) }
func (a *countA) Result() (float64, bool)     { return float64(a.n), a.n > 0 }
func (a *countA) NeedsMembersOnDelete() bool  { return false }

type sumA struct {
	n int
	s float64
}

func (a *sumA) Kind() Kind  { return Sum }
func (a *sumA) Add(v float64) { a.n++; a.s += v }
func (a *sumA) Remove(v float64) bool {
	a.n--
	a.s -= v
	return false
}
func (a *sumA) Rebuild(vs []float64) {
	a.n, a.s = len(vs), 0
	for _, v := range vs {
		a.s += v
	}
}
func (a *sumA) Result() (float64, bool)    { return a.s, a.n > 0 }
func (a *sumA) NeedsMembersOnDelete() bool { return false }

type minMaxA struct {
	min    bool
	n      int
	ext    float64
	extBit uint64
}

func (a *minMaxA) Kind() Kind {
	if a.min {
		return Min
	}
	return Max
}

func (a *minMaxA) Add(v float64) {
	b := math.Float64bits(v)
	switch {
	case a.n == 0:
		a.ext, a.extBit = v, b
	case a.min && b < a.extBit:
		a.ext, a.extBit = v, b
	case !a.min && b > a.extBit:
		a.ext, a.extBit = v, b
	}
	a.n++
}

func (a *minMaxA) Remove(v float64) bool {
	a.n--
	return math.Float64bits(v) == a.extBit
}

func (a *minMaxA) Rebuild(vs []float64) {
	a.n = 0
	for _, v := range vs {
		a.Add(v)
	}
}

func (a *minMaxA) Result() (float64, bool)    { return a.ext, a.n > 0 }
func (a *minMaxA) NeedsMembersOnDelete() bool { return true }

type distinctA struct {
	n int
	m map[uint64]struct{}
}

func (a *distinctA) Kind() Kind { return DistinctCount }
func (a *distinctA) Add(v float64) {
	b := math.Float64bits(v)
	if _, ok := a.m[b]; !ok {
		a.m[b] = struct{}{}
	}
	a.n++
}
func (a *distinctA) Remove(float64) bool {
	a.n--
	return true
}
func (a *distinctA) Rebuild(vs []float64) {
	a.n, a.m = len(vs), map[uint64]struct{}{}
	for _, v := range vs {
		a.m[math.Float64bits(v)] = struct{}{}
	}
}
func (a *distinctA) Result() (float64, bool) {
	return float64(len(a.m)), a.n > 0
}
func (a *distinctA) NeedsMembersOnDelete() bool { return true }
